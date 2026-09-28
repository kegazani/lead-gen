package app

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sort"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/mail"
	"lead-gen/contracts/integration-platform/search"
)

// testTime — фиксированный момент для детерминированных тестов.
var testTime = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)

// fakeClock — детерминированные часы.
type fakeClock struct{ t time.Time }

func (c fakeClock) Now() time.Time { return c.t }

// fakeSearch — фейк порта поиска.
type fakeSearch struct {
	results []search.Result
	err     error
	queries []search.Query
	// byQuery — результаты, зависящие от текста запроса (если заданы). Иначе
	// возвращаются results.
	byQuery map[string][]search.Result
}

func (f *fakeSearch) Search(_ context.Context, q search.Query) ([]search.Result, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	if f.byQuery != nil {
		return f.byQuery[q.Text], nil
	}
	return f.results, nil
}

// fakeCrawl — фейк порта обхода со счётчиком вызовов Extract.
type fakeCrawl struct {
	pages         map[string][]crawl.Page
	extract       map[string][]crawl.ContactCandidate
	requests      []crawl.Request
	crawlErr      error
	crawlErrByURL map[string]error
	extractErr    error
	extractCalls  int
}

// testCrawlLimits — пределы обхода, зеркалящие конфигурацию сервиса: глубина и
// время — операторские/конфигурационные лимиты (не обязательны для запуска),
// страницы — внутренний предохранитель на сайт.
func testCrawlLimits() CrawlLimits {
	return CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120,
		IncludePaths: []string{"contact", "team", "about"}}
}

func (f *fakeCrawl) Crawl(_ context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	f.requests = append(f.requests, req)
	if err, ok := f.crawlErrByURL[req.StartURL]; ok {
		return crawl.CrawlOutcome{}, err
	}
	if f.crawlErr != nil {
		return crawl.CrawlOutcome{}, f.crawlErr
	}
	return crawl.CrawlOutcome{Pages: f.pages[req.StartURL]}, nil
}

func (f *fakeCrawl) Extract(_ context.Context, req crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	f.extractCalls++
	if f.extractErr != nil {
		return nil, f.extractErr
	}
	return f.extract[req.URL], nil
}

// fakeLLM — фейк порта LLM.
type fakeLLM struct {
	targets        []llm.Target
	suggestions    []llm.Suggestion
	converse       []llm.ConverseReply
	interpretation llm.Interpretation
	assessment     llm.Assessment
	// assessQueue — очередь заготовленных оценок: если непуста, Assess отдаёт
	// ответы по порядку (для проверки ревизии оценки при повторной обработке).
	assessQueue   []llm.Assessment
	assessIdx     int
	converseIdx   int
	converseCalls int
	planCalls     int
	// planRequests — зафиксированные запросы планирования: позволяют проверить,
	// что интерпретация задания дошла до llm.Port.Plan.
	planRequests []llm.PlanRequest
	// planQueue — заготовленные ответы Plan по порядку вызовов (для проверки
	// добора целей); когда исчерпана, повторяется последний ответ.
	planQueue [][]llm.Target
	usage     llm.Usage
	err       error
}

func (f *fakeLLM) Plan(_ context.Context, req llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	call := f.planCalls
	f.planCalls++
	f.planRequests = append(f.planRequests, req)
	if f.err != nil {
		return nil, llm.Usage{}, f.err
	}
	if len(f.planQueue) > 0 {
		i := call
		if i >= len(f.planQueue) {
			i = len(f.planQueue) - 1
		}
		return f.planQueue[i], f.usage, nil
	}
	return f.targets, f.usage, nil
}

func (f *fakeLLM) Suggest(_ context.Context, _ llm.SuggestRequest) ([]llm.Suggestion, llm.Usage, error) {
	if f.err != nil {
		return nil, llm.Usage{}, f.err
	}
	return f.suggestions, llm.Usage{}, nil
}

// Interpret отдаёт заготовленные критерии, а без них — валидную интерпретацию с
// явной неопределённостью и без выдуманных критериев.
func (f *fakeLLM) Interpret(_ context.Context, _ llm.InterpretRequest) (llm.Interpretation, llm.Usage, error) {
	if f.err != nil {
		return llm.Interpretation{}, llm.Usage{}, f.err
	}
	if f.interpretation.Uncertainty || f.interpretation.TargetLead != "" {
		return f.interpretation, llm.Usage{}, nil
	}
	return llm.Interpretation{Uncertainty: true}, llm.Usage{}, nil
}

// Assess отдаёт заготовленную оценку, а без неё — валидную гипотезу по профилю с
// недостающим свидетельством потребности.
func (f *fakeLLM) Assess(_ context.Context, _ llm.AssessRequest) (llm.Assessment, llm.Usage, error) {
	if f.err != nil {
		return llm.Assessment{}, llm.Usage{}, f.err
	}
	if len(f.assessQueue) > 0 {
		i := f.assessIdx
		if i >= len(f.assessQueue) {
			i = len(f.assessQueue) - 1
		}
		f.assessIdx++
		return f.assessQueue[i], llm.Usage{}, nil
	}
	if f.assessment.Verdict != "" {
		return f.assessment, llm.Usage{}, nil
	}
	return llm.Assessment{
		Verdict:    llm.VerdictHypothesis,
		SignalKind: llm.SignalProfile,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "профиль",
			Fact:       "отрасль совпадает",
			SourceURL:  "https://example.com/about",
			Reasoning:  "компания из нужной отрасли",
			SignalKind: llm.SignalProfile,
			Missing:    "нет заявления о потребности",
		}},
	}, llm.Usage{}, nil
}

// Converse отдаёт заготовленные реплики по порядку, а когда они кончились —
// повторяет последнюю. Без заготовок возвращает нейтральный уточняющий вопрос.
func (f *fakeLLM) Converse(_ context.Context, _ llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	f.converseCalls++
	if f.err != nil {
		return llm.ConverseReply{}, llm.Usage{}, f.err
	}
	if len(f.converse) == 0 {
		return llm.ConverseReply{Text: "какой регион нужен?"}, llm.Usage{}, nil
	}
	i := f.converseIdx
	if i >= len(f.converse) {
		i = len(f.converse) - 1
	}
	f.converseIdx++
	return f.converse[i], f.usage, nil
}

// ConverseStream отдаёт заготовленную реплику потоком: единый фрагмент текста и
// завершающий кадр с ответом и расходом токенов.
func (f *fakeLLM) ConverseStream(_ context.Context, req llm.ConverseRequest) iter.Seq2[llm.ConverseDelta, error] {
	return func(yield func(llm.ConverseDelta, error) bool) {
		reply, usage, err := f.Converse(context.Background(), req)
		if err != nil {
			yield(llm.ConverseDelta{}, err)
			return
		}
		if !yield(llm.ConverseDelta{Text: reply.Text}, nil) {
			return
		}
		yield(llm.ConverseDelta{Reply: &reply, Usage: usage}, nil)
	}
}

// fakeForum — фейк порта форумного источника.
type fakeForum struct {
	signals []forum.Signal
	err     error
}

func (f *fakeForum) SearchSignals(_ context.Context, _ forum.Query) ([]forum.Signal, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.signals, nil
}

// memProfiles — in-memory репозиторий карточек.
type memProfiles struct {
	byID map[domain.CompanyID]*domain.Company
}

func newMemProfiles() *memProfiles {
	return &memProfiles{byID: make(map[domain.CompanyID]*domain.Company)}
}

func (m *memProfiles) Save(_ context.Context, c *domain.Company) error {
	m.byID[c.ID()] = c
	return nil
}

func (m *memProfiles) FindByID(_ context.Context, id domain.CompanyID) (*domain.Company, error) {
	if c, ok := m.byID[id]; ok {
		return c, nil
	}
	return nil, ports.ErrProfileNotFound
}

func (m *memProfiles) ListVisible(_ context.Context, limit int) ([]*domain.Company, error) {
	var out []*domain.Company
	for _, c := range m.byID {
		if c.Hidden() {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memProfiles) ListSnoozed(_ context.Context) ([]*domain.Company, error) {
	var out []*domain.Company
	for _, c := range m.byID {
		if c.Hidden() {
			continue
		}
		if c.Status().SnoozeUntil != nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (m *memProfiles) Delete(_ context.Context, id domain.CompanyID) error {
	if _, ok := m.byID[id]; !ok {
		return ports.ErrProfileNotFound
	}
	delete(m.byID, id)
	return nil
}

// memMessages — in-memory репозиторий исходящих писем.
type memMessages struct {
	byID map[domain.OutboundMessageID]*domain.OutboundMessage
}

func newMemMessages() *memMessages {
	return &memMessages{byID: make(map[domain.OutboundMessageID]*domain.OutboundMessage)}
}

func (m *memMessages) Save(_ context.Context, message *domain.OutboundMessage) error {
	m.byID[message.ID()] = message
	return nil
}

func (m *memMessages) FindByID(_ context.Context, id domain.OutboundMessageID) (*domain.OutboundMessage, error) {
	if message, ok := m.byID[id]; ok {
		return message, nil
	}
	return nil, ports.ErrMessageNotFound
}

func (m *memMessages) ListByLead(_ context.Context, leadID domain.CompanyID) ([]*domain.OutboundMessage, error) {
	var out []*domain.OutboundMessage
	for _, message := range m.byID {
		if message.LeadID() == leadID {
			out = append(out, message)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (m *memMessages) SuppressedLeads(_ context.Context) ([]domain.CompanyID, error) {
	set := map[domain.CompanyID]bool{}
	for _, message := range m.byID {
		if message.Status().Suppresses() {
			set[message.LeadID()] = true
		}
	}
	var out []domain.CompanyID
	for leadID := range set {
		out = append(out, leadID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// memActivity — in-memory хранилище истории переходов.
type memActivity struct {
	entries []ports.LeadActivity
	next    int
}

func newMemActivity() *memActivity {
	return &memActivity{}
}

func (m *memActivity) RecordStageChange(_ context.Context, leadID domain.CompanyID, stage domain.Stage, author string, at time.Time) error {
	m.next++
	m.entries = append(m.entries, ports.LeadActivity{
		ID:     fmt.Sprintf("a-%d", m.next),
		LeadID: leadID,
		Stage:  stage,
		Author: author,
		At:     at,
	})
	return nil
}

func (m *memActivity) ListByLead(_ context.Context, leadID domain.CompanyID) ([]ports.LeadActivity, error) {
	var out []ports.LeadActivity
	for _, entry := range m.entries {
		if entry.LeadID == leadID {
			out = append(out, entry)
		}
	}
	return out, nil
}

// memTemplates — in-memory репозиторий шаблонов письма для тестов прикладного
// слоя: хранит снимки агрегата и повторяет семантику поиска порта.
type memTemplates struct {
	byID map[domain.TemplateID]*domain.EmailTemplate
}

func newMemTemplates() *memTemplates {
	return &memTemplates{byID: make(map[domain.TemplateID]*domain.EmailTemplate)}
}

func (m *memTemplates) Save(_ context.Context, template *domain.EmailTemplate) error {
	m.byID[template.ID()] = template
	return nil
}

func (m *memTemplates) FindByID(_ context.Context, id domain.TemplateID) (*domain.EmailTemplate, error) {
	if template, ok := m.byID[id]; ok {
		return template, nil
	}
	return nil, ports.ErrTemplateNotFound
}

func (m *memTemplates) Delete(_ context.Context, id domain.TemplateID) error {
	if _, ok := m.byID[id]; !ok {
		return ports.ErrTemplateNotFound
	}
	delete(m.byID, id)
	return nil
}

func (m *memTemplates) Search(_ context.Context, query string, limit int) ([]*domain.EmailTemplate, error) {
	normalized := ports.NormalizeTemplateQuery(query)
	var out []*domain.EmailTemplate
	for _, template := range m.byID {
		if ports.TemplateMatches(template, normalized) {
			out = append(out, template)
		}
	}
	ports.SortTemplates(out)
	return ports.ApplyTemplateLimit(out, limit), nil
}

// fakeMail — фейк порта отправки писем.
type fakeMail struct {
	sent []mail.Message
	err  error
}

func (f *fakeMail) Send(_ context.Context, message mail.Message) error {
	f.sent = append(f.sent, message)
	return f.err
}

// memContacts — in-memory репозиторий контактов.
type memContacts struct {
	byCompany map[domain.CompanyID]map[domain.ContactID]*domain.Contact
}

func newMemContacts() *memContacts {
	return &memContacts{byCompany: make(map[domain.CompanyID]map[domain.ContactID]*domain.Contact)}
}

func (m *memContacts) Save(_ context.Context, c *domain.Contact) error {
	if m.byCompany[c.CompanyID()] == nil {
		m.byCompany[c.CompanyID()] = make(map[domain.ContactID]*domain.Contact)
	}
	m.byCompany[c.CompanyID()][c.ID()] = c
	return nil
}

func (m *memContacts) FindByID(_ context.Context, id domain.ContactID) (*domain.Contact, error) {
	for _, company := range m.byCompany {
		if c, ok := company[id]; ok {
			return c, nil
		}
	}
	return nil, errors.New("contact not found")
}

func (m *memContacts) ListByCompany(_ context.Context, companyID domain.CompanyID) ([]*domain.Contact, error) {
	var out []*domain.Contact
	for _, c := range m.byCompany[companyID] {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (m *memContacts) ListByCompanies(_ context.Context, companyIDs []domain.CompanyID) ([]*domain.Contact, error) {
	var out []*domain.Contact
	for _, companyID := range companyIDs {
		for _, c := range m.byCompany[companyID] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// memJobs — in-memory хранилище заданий поиска (и jobs.Store, и SearchJobStore).
type memJobs struct {
	byID    map[string]*ports.SearchJob
	payload map[string][]byte
	order   []string
	next    int
	now     time.Time
	// cancelRequests — отметки запроса отмены для running-заданий (queued сразу
	// переходят в cancelled).
	cancelRequests map[string]bool
	// progressHistory — все опубликованные снимки прогресса по заданиям (для
	// проверки последовательности фаз).
	progressHistory map[string][]ports.SearchJobProgress
	// events — записанные события жизненного цикла.
	events []ports.SearchJobEvent
}

func newMemJobs() *memJobs {
	return &memJobs{
		byID:            make(map[string]*ports.SearchJob),
		payload:         make(map[string][]byte),
		now:             testTime,
		cancelRequests:  make(map[string]bool),
		progressHistory: make(map[string][]ports.SearchJobProgress),
	}
}

func (m *memJobs) CreateSearchJob(_ context.Context, query string, maxCredits int) (ports.SearchJob, error) {
	return m.createJob(query, nil, maxCredits, 0, 0)
}

func (m *memJobs) CreateSearchJobWithPlan(_ context.Context, query string, plan ports.SearchPlan, maxCredits, targetCompanies, maxResults int) (ports.SearchJob, error) {
	return m.createJob(query, &plan, maxCredits, targetCompanies, maxResults)
}

// createJob — общая постановка задания в очередь с необязательным планом.
func (m *memJobs) createJob(query string, plan *ports.SearchPlan, maxCredits, targetCompanies, maxResults int) (ports.SearchJob, error) {
	m.next++
	id := fmt.Sprintf("job-%d", m.next)
	j := ports.SearchJob{
		ID:         id,
		Query:      query,
		Status:     jobs.StatusQueued,
		MaxCredits: maxCredits,
		CreatedAt:  m.now,
		UpdatedAt:  m.now,
	}
	m.byID[id] = &j
	m.order = append(m.order, id)
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: query, MaxCredits: maxCredits, TargetCompanies: targetCompanies, MaxResults: maxResults, Plan: plan})
	if err != nil {
		return ports.SearchJob{}, err
	}
	m.payload[id] = payload
	return j, nil
}

func (m *memJobs) RecordResult(_ context.Context, id string, resultRef []string) error {
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	j.ResultRef = append([]string(nil), resultRef...)
	return nil
}

func (m *memJobs) RecordBudget(_ context.Context, id string, b budget.Snapshot) error {
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	copy := b
	j.Budget = &copy
	return nil
}

func (m *memJobs) RecordProgress(_ context.Context, id string, progress ports.SearchJobProgress) error {
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	copy := progress
	j.Progress = &copy
	m.progressHistory[id] = append(m.progressHistory[id], copy)
	return nil
}

func (m *memJobs) ListSearchJobs(_ context.Context, limit int) ([]ports.SearchJob, error) {
	out := make([]ports.SearchJob, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		out = append(out, *m.byID[m.order[i]])
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// jobs.Store

func (m *memJobs) Create(_ context.Context, job jobs.Job) error {
	j := ports.SearchJob{ID: job.ID, Status: job.Status, CreatedAt: m.now, UpdatedAt: m.now}
	m.byID[job.ID] = &j
	m.payload[job.ID] = append([]byte(nil), job.Payload...)
	m.order = append(m.order, job.ID)
	return nil
}

func (m *memJobs) TakeQueued(_ context.Context) (*jobs.Job, error) {
	for _, id := range m.order {
		j := m.byID[id]
		if j.Status == jobs.StatusQueued {
			j.Status = jobs.StatusRunning
			j.UpdatedAt = m.now
			return &jobs.Job{ID: id, Status: jobs.StatusRunning, Payload: m.payload[id]}, nil
		}
	}
	return nil, jobs.ErrNoJob
}

func (m *memJobs) FindByID(_ context.Context, id string) (*jobs.Job, error) {
	j, ok := m.byID[id]
	if !ok {
		return nil, jobs.ErrNotFound
	}
	return &jobs.Job{
		ID:              j.ID,
		Status:          j.Status,
		Payload:         m.payload[id],
		LastError:       j.Error,
		ErrorCode:       j.ErrorCode,
		CancelRequested: m.cancelRequests[id],
		CreatedAt:       j.CreatedAt,
		UpdatedAt:       j.UpdatedAt,
	}, nil
}

func (m *memJobs) UpdateStatus(_ context.Context, id string, from, to jobs.Status, reason string, code string) error {
	j, ok := m.byID[id]
	if !ok {
		return jobs.ErrNotFound
	}
	if j.Status != from {
		return jobs.ErrTransitionRejected
	}
	j.Status = to
	j.Error = reason
	j.ErrorCode = code
	j.UpdatedAt = m.now
	return nil
}

// ReconcileRunning согласует осиротевшие running-задания. Фейк моделирует аренду
// по UpdatedAt и отмену, но не лимит попыток: прикладные тесты согласование не
// упражняют, а точная политика проверяется контрактными тестами хранилищ.
func (m *memJobs) ReconcileRunning(_ context.Context, staleBefore time.Time, _ int, _ string, _ string) (jobs.ReconcileResult, error) {
	var result jobs.ReconcileResult
	for _, id := range m.order {
		j := m.byID[id]
		if j.Status != jobs.StatusRunning {
			continue
		}
		if j.UpdatedAt.After(staleBefore) {
			continue
		}
		if m.cancelRequests[id] {
			j.Status = jobs.StatusCancelled
			result.Cancelled++
		} else {
			j.Status = jobs.StatusQueued
			result.Requeued++
		}
		j.UpdatedAt = m.now
	}
	return result, nil
}

func (m *memJobs) RecordEvent(_ context.Context, event ports.SearchJobEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *memJobs) ListEvents(_ context.Context, jobID string) ([]ports.SearchJobEvent, error) {
	var out []ports.SearchJobEvent
	for _, e := range m.events {
		if e.JobID == jobID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memJobs) RequestCancel(_ context.Context, id string) error {
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	switch j.Status {
	case jobs.StatusQueued:
		j.Status = jobs.StatusCancelled
	case jobs.StatusRunning:
		j.Status = jobs.StatusCancelled
		m.cancelRequests[id] = true
	default:
		// Терминальное состояние — no-op.
		return nil
	}
	j.UpdatedAt = m.now
	return nil
}

// memAssessments — in-memory фейк хранилища оценок для прикладных тестов.
type memAssessments struct {
	byKey  map[string]*domain.LeadAssessment
	saveEr error
}

func newMemAssessments() *memAssessments {
	return &memAssessments{byKey: make(map[string]*domain.LeadAssessment)}
}

func assessmentKeyParts(jobID, companyID string) string { return jobID + "\x00" + companyID }

func (m *memAssessments) Save(_ context.Context, assessment *domain.LeadAssessment) error {
	if m.saveEr != nil {
		return m.saveEr
	}
	m.byKey[assessmentKeyParts(assessment.JobID(), assessment.CompanyID())] = assessment
	return nil
}

func (m *memAssessments) GetByJobAndCompany(_ context.Context, jobID, companyID string) (*domain.LeadAssessment, error) {
	a, ok := m.byKey[assessmentKeyParts(jobID, companyID)]
	if !ok {
		return nil, ports.ErrAssessmentNotFound
	}
	return a, nil
}

func (m *memAssessments) ListByJob(_ context.Context, jobID string) ([]*domain.LeadAssessment, error) {
	out := make([]*domain.LeadAssessment, 0)
	for _, a := range m.byKey {
		if a.JobID() == jobID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CompanyID() < out[j].CompanyID() })
	return out, nil
}

func (m *memAssessments) VerdictDistribution(_ context.Context, jobID string) (ports.VerdictCounts, error) {
	var counts ports.VerdictCounts
	for _, a := range m.byKey {
		if a.JobID() != jobID {
			continue
		}
		switch a.Verdict() {
		case domain.VerdictFits:
			counts.Fits++
		case domain.VerdictHypothesis:
			counts.Hypothesis++
		case domain.VerdictUnsuitable:
			counts.Unsuitable++
		}
	}
	return counts, nil
}

var _ ports.LeadAssessmentStore = (*memAssessments)(nil)
