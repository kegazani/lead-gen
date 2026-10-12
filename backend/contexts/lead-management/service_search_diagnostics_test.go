package leadmanagement

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/search"
)

const (
	assembledNormalizerCode = "assembled.search.failure"
	assembledNormalizerText = "Безопасная причина собранного сервиса"
	assembledDiagnostic     = "assembled_search_diagnostic"
)

type assembledFailureNormalizer struct {
	calls int
	got   []error
}

func (n *assembledFailureNormalizer) Normalize(err error) jobs.Failure {
	n.calls++
	n.got = append(n.got, err)
	return jobs.Failure{Code: assembledNormalizerCode, Message: assembledNormalizerText, Diagnostic: assembledDiagnostic}
}

type assembledSearchFailure struct {
	err     error
	results []search.Result
}

func (s assembledSearchFailure) Search(context.Context, search.Query) ([]search.Result, error) {
	return s.results, s.err
}

type assembledDiagnosticsCrawl struct{}

func (assembledDiagnosticsCrawl) Crawl(_ context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	return crawl.CrawlOutcome{Pages: []crawl.Page{{
		URL: req.StartURL, Metadata: map[string]string{"title": "Diagnostics Company", "description": "A safe description"},
		Markdown: "# Diagnostics Company\n\nA safe description\n\nhello@diagnostics.example",
	}}}, nil
}

type assembledPageCrawl struct{ page crawl.Page }

func (c assembledPageCrawl) Crawl(context.Context, crawl.Request) (crawl.CrawlOutcome, error) {
	return crawl.CrawlOutcome{Pages: []crawl.Page{c.page}}, nil
}

func (assembledPageCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

func (assembledDiagnosticsCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

type assembledFailingBudget struct{ err error }

func (b assembledFailingBudget) Begin(string, int) (budget.Task, error) { return nil, b.err }
func (b assembledFailingBudget) Finish(context.Context, string) error   { return nil }

func TestServiceWorkerUsesAssembledFailureNormalizerEndToEnd(t *testing.T) {
	const (
		secret = "service-secret-9c17"
		pii    = "person.private@example.test"
		query  = "service-safe-query"
	)
	providerErr := fmt.Errorf("provider failure: %s %s", secret, pii)
	var output bytes.Buffer
	logger := &errorArgLogger{Logger: observ.NewLoggerWithWriter(&output)}
	metrics := observ.NewMetrics()
	store := memory.NewSearchJobStore()
	normalizer := &assembledFailureNormalizer{}

	svc, err := New(Config{
		Profiles:          memory.NewLeadProfileRepository(),
		Contacts:          memory.NewContactRepository(),
		Jobs:              store,
		Messages:          memory.NewOutboundMessageRepository(),
		Templates:         memory.NewEmailTemplateRepository(),
		Activity:          memory.NewLeadActivityStore(),
		Search:            assembledSearchFailure{},
		Crawl:             assembledDiagnosticsCrawl{},
		Budget:            assembledFailingBudget{err: providerErr},
		Mail:              noopMailPort{},
		Logger:            logger,
		Metrics:           metrics,
		Tracer:            observ.NewTracer(ServiceID),
		FailureNormalizer: normalizer,
		Interval:          5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if svc.runner.FailureNormalizer != normalizer {
		t.Fatal("собранный Runner получил не внедрённый экземпляр FailureNormalizer")
	}

	job, err := store.CreateSearchJob(context.Background(), query, 10)
	if err != nil {
		t.Fatalf("создать задание в durable memory store: %v", err)
	}
	jobID := job.ID
	worker, ok := svc.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("у сервиса отсутствует публичная роль worker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		stored, findErr := store.FindByID(context.Background(), jobID)
		if findErr != nil {
			cancel()
			<-done
			t.Fatalf("опрос состояния задания: %v", findErr)
		}
		if stored.Status.Terminal() {
			if stored.Status != jobs.StatusFailed {
				cancel()
				<-done
				t.Fatalf("статус задания = %q, ожидался failed", stored.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("публичный RoleRunner.Run не завершил задание за отведённое время")
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка RoleRunner.Run по отмене: %v", err)
	}

	if normalizer.calls != 2 {
		t.Fatalf("внедрённый нормализатор вызван %d раз, ожидалось отдельно RunSearch и jobs.Runner", normalizer.calls)
	}
	for i, got := range normalizer.got {
		if got == nil || !strings.Contains(got.Error(), secret) {
			t.Errorf("вызов нормализатора %d получил не исходную ошибку: %v", i, got)
		}
	}
	if len(logger.errorArgs) != 2 || logger.errorArgs[0] != nil || logger.errorArgs[1] != nil {
		t.Fatalf("Logger.Error получил аргументы ошибок %v, ожидались два nil", logger.errorArgs)
	}
	stored, err := store.FindByID(context.Background(), jobID)
	if err != nil {
		t.Fatalf("повторно прочитать завершённое задание: %v", err)
	}
	if stored.ErrorCode != assembledNormalizerCode || stored.LastError != assembledNormalizerText {
		t.Errorf("в задании сохранён небезопасный/неверный отказ: code=%q reason=%q", stored.ErrorCode, stored.LastError)
	}
	events, err := store.ListEvents(context.Background(), jobID)
	if err != nil {
		t.Fatalf("прочитать события задания: %v", err)
	}
	var errorEventCodes []string
	for _, event := range events {
		if event.Type == ports.EventError {
			errorEventCodes = append(errorEventCodes, event.Code)
		}
	}
	if len(errorEventCodes) != 1 || errorEventCodes[0] != "budget.exhausted" {
		t.Errorf("EventError.Code = %v, ожидался [budget.exhausted]", errorEventCodes)
	}

	entries := decodeJSONLogLines(t, output.String())
	var fatal, runnerFailure map[string]any
	for _, entry := range entries {
		if entry["msg"] == "задание упало" {
			fatal = entry
		}
		if entry["msg"] == "задание не выполнено" {
			runnerFailure = entry
		}
	}
	for stage, entry := range map[string]map[string]any{"RunSearch": fatal, "jobs.Runner": runnerFailure} {
		if entry == nil {
			t.Errorf("нет JSON-записи фатального отказа этапа %s: %s", stage, output.String())
			continue
		}
		for key, want := range map[string]string{
			"error_code": assembledNormalizerCode,
			"reason":     assembledNormalizerText,
			"diagnostic": assembledDiagnostic,
			"task_id":    jobID,
		} {
			if entry[key] != want {
				t.Errorf("этап %s: %s = %v, ожидалось %q", stage, key, entry[key], want)
			}
		}
	}
	for _, forbidden := range []string{secret, pii} {
		if strings.Contains(output.String(), forbidden) {
			t.Errorf("JSON-лог содержит исходное значение %q: %s", forbidden, output.String())
		}
	}
	if got := metrics.(*observ.MemoryMetrics).Counter("jobs_total", map[string]string{"service": ServiceID, "outcome": "failed"}); got != 1 {
		t.Errorf("счётчик отказов задания = %d, ожидался 1", got)
	}
}

func TestServiceWorkerCancellationDuringCrawlIsNotFailure(t *testing.T) {
	store := memory.NewSearchJobStore()
	crawlPort := &assembledWaitingCrawl{started: make(chan struct{})}
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	var output bytes.Buffer
	service, err := New(Config{
		Profiles: memory.NewLeadProfileRepository(), Contacts: memory.NewContactRepository(), Jobs: store,
		Messages: memory.NewOutboundMessageRepository(), Templates: memory.NewEmailTemplateRepository(),
		Activity: memory.NewLeadActivityStore(), Search: assembledSearchFailure{}, Crawl: crawlPort,
		Mail: noopMailPort{}, Logger: observ.NewLoggerWithWriter(&output), Metrics: metrics,
		Tracer: observ.NewTracer(ServiceID), Interval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	job, err := store.CreateSearchJobWithPlan(context.Background(), "cancel during crawl", ports.SearchPlan{
		Targets: []ports.PlanTarget{{URL: "https://cancel.example"}},
	}, 0, 0, 0)
	if err != nil {
		t.Fatalf("создать задание: %v", err)
	}
	worker, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("у сервиса отсутствует публичная роль worker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	select {
	case <-crawlPort.started:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("обработка не дошла до блокирующего Crawl")
	}
	if err := store.RequestCancel(context.Background(), job.ID); err != nil {
		cancel()
		<-done
		t.Fatalf("запросить отмену выполняющегося задания: %v", err)
	}
	if err := waitServiceJobTerminal(store, job.ID, jobs.StatusCancelled); err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RoleRunner.Run после отмены: %v", err)
	}
	stored, err := store.FindByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("прочитать отменённое задание: %v", err)
	}
	if stored.Status != jobs.StatusCancelled {
		t.Fatalf("статус после отмены во время Crawl = %q, ожидался cancelled", stored.Status)
	}
	if got := metrics.Counter("jobs_total", map[string]string{"service": ServiceID, "outcome": "cancelled"}); got != 1 {
		t.Errorf("счётчик отмен = %d, ожидался 1", got)
	}
	if got := metrics.Counter("jobs_total", map[string]string{"service": ServiceID, "outcome": "failed"}); got != 0 {
		t.Errorf("отмена была учтена как отказ: jobs_total failed = %d", got)
	}
	events, err := store.ListEvents(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("прочитать события отменённого задания: %v", err)
	}
	for _, event := range events {
		if event.Type == ports.EventError {
			t.Errorf("отмена породила EventError: %+v", event)
		}
	}
}

type assembledWaitingCrawl struct{ started chan struct{} }

func (c *assembledWaitingCrawl) Crawl(ctx context.Context, _ crawl.Request) (crawl.CrawlOutcome, error) {
	close(c.started)
	<-ctx.Done()
	return crawl.CrawlOutcome{}, crawl.ErrCancelled
}

func (*assembledWaitingCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

func TestServicePageProcessedInfoOmitsSensitiveURLAndIdentity(t *testing.T) {
	const (
		querySentinel    = "query-sentinel-2a71"
		userSentinel     = "userinfo-sentinel-0d34"
		pathSentinel     = "path-sentinel-14fa"
		fragmentSentinel = "fragment-sentinel-997c"
		domainSentinel   = "domain-sentinel-1a31.example"
		companySentinel  = "company-sentinel-c84e"
	)
	pageURL := "https://" + userSentinel + "@" + domainSentinel + "/" + pathSentinel + "#" + fragmentSentinel
	var output bytes.Buffer
	store := memory.NewSearchJobStore()
	service, err := New(Config{
		Profiles: memory.NewLeadProfileRepository(), Contacts: memory.NewContactRepository(), Jobs: store,
		Messages: memory.NewOutboundMessageRepository(), Templates: memory.NewEmailTemplateRepository(),
		Activity: memory.NewLeadActivityStore(), Search: assembledSearchFailure{},
		Crawl: assembledPageCrawl{page: crawl.Page{URL: pageURL, Metadata: map[string]string{
			"title": companySentinel, "description": "safe description",
		}, Markdown: "# " + companySentinel + "\n\nsafe description\n\ncontact@safe.example"}},
		Mail: noopMailPort{}, Logger: observ.NewLoggerWithWriter(&output), Metrics: observ.NewMetrics(),
		Tracer: observ.NewTracer(ServiceID), Interval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	storeJob, err := store.CreateSearchJobWithPlan(context.Background(), querySentinel, ports.SearchPlan{
		Targets: []ports.PlanTarget{{URL: pageURL}},
	}, 0, 0, 0)
	if err != nil {
		t.Fatalf("создать задание: %v", err)
	}
	worker, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("у сервиса отсутствует публичная роль worker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	if err := waitServiceJobTerminal(store, storeJob.ID, jobs.StatusDone); err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка worker: %v", err)
	}
	entries := decodeJSONLogLines(t, output.String())
	var processed map[string]any
	for _, entry := range entries {
		if entry["msg"] == "поиск: страница обработана" {
			processed = entry
			break
		}
	}
	if processed == nil {
		t.Fatalf("не найден Info «страница обработана»: %s", output.String())
	}
	if processed["task_id"] != storeJob.ID || processed["service"] != ServiceID {
		t.Errorf("в Info отсутствует безопасная корреляция: %+v", processed)
	}
	for _, key := range []string{"domain", "company", "company_id"} {
		if _, present := processed[key]; present {
			t.Errorf("Info содержит запрещённый атрибут %q: %+v", key, processed)
		}
	}
	for _, forbidden := range []string{querySentinel, userSentinel, pathSentinel, fragmentSentinel, domainSentinel, companySentinel, pageURL} {
		if strings.Contains(output.String(), forbidden) {
			t.Errorf("логи содержат запрещённое значение %q: %s", forbidden, output.String())
		}
	}
}

func waitServiceJobTerminal(store *memory.SearchJobStore, id string, want jobs.Status) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stored, err := store.FindByID(context.Background(), id)
		if err != nil {
			return fmt.Errorf("опрос задания: %w", err)
		}
		if stored.Status.Terminal() {
			if stored.Status != want {
				return fmt.Errorf("статус задания = %q, ожидался %q", stored.Status, want)
			}
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return errors.New("истёк таймаут ожидания терминального статуса задания")
}

func TestServiceNewRejectsNilLogger(t *testing.T) {
	cfg := serviceDiagnosticsConfig()
	cfg.Logger = nil
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "Logger обязателен") {
		t.Fatalf("New с nil Logger вернул ошибку %v; ожидалась обязательная зависимость", err)
	}
}

func TestServiceAssessmentReadWarningUsesFixedSafeDiagnostic(t *testing.T) {
	const (
		secret = "assessment-read-secret-54e2"
		pii    = "private.person@example.test"
		query  = "safe assessment query"
	)
	var output bytes.Buffer
	logger := &errorArgLogger{Logger: observ.NewLoggerWithWriter(&output)}
	store := memory.NewSearchJobStore()
	assessments := assembledAssessmentReadFailure{err: fmt.Errorf("assessment store: %s %s", secret, pii)}
	svc, err := New(Config{
		Profiles: memory.NewLeadProfileRepository(), Contacts: memory.NewContactRepository(),
		Jobs: store, Messages: memory.NewOutboundMessageRepository(), Templates: memory.NewEmailTemplateRepository(),
		Activity: memory.NewLeadActivityStore(),
		Search:   assembledSearchFailure{results: []search.Result{{URL: "https://assessment-warning.example"}}},
		Crawl:    assembledDiagnosticsCrawl{}, LLM: assembledAssessmentLLM{}, Assessments: assessments,
		Mail: noopMailPort{}, Logger: logger, Metrics: observ.NewMetrics(), Tracer: observ.NewTracer(ServiceID),
		Interval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	job, err := store.CreateSearchJobWithPlan(context.Background(), query, ports.SearchPlan{
		Targets: []ports.PlanTarget{{Query: query}},
	}, 0, 0, 0)
	if err != nil {
		t.Fatalf("создать задание: %v", err)
	}
	worker, ok := svc.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("у сервиса отсутствует публичная роль worker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stored, findErr := store.FindByID(context.Background(), job.ID)
		if findErr != nil {
			cancel()
			<-done
			t.Fatalf("опрос задания: %v", findErr)
		}
		if stored.Status.Terminal() {
			if stored.Status != jobs.StatusDone {
				cancel()
				<-done
				t.Fatalf("ошибка чтения оценки изменила исход задания: %q", stored.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("публичный worker не завершил задачу оценки за отведённое время")
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка worker: %v", err)
	}
	if len(logger.errorArgs) != 0 {
		t.Fatalf("предупреждение чтения оценки вызвало Logger.Error с аргументами %v", logger.errorArgs)
	}
	entries := decodeJSONLogLines(t, output.String())
	var warning map[string]any
	for _, entry := range entries {
		if entry["msg"] == "поиск: чтение прежней оценки компании" {
			warning = entry
			break
		}
	}
	if warning == nil {
		t.Fatalf("нет JSON-предупреждения о безопасном этапе чтения оценки: %s", output.String())
	}
	if warning["diagnostic_code"] != "assessment_read_failed" || warning["task_id"] != job.ID {
		t.Errorf("предупреждение не содержит фиксированный этап и task_id: %+v", warning)
	}
	for _, forbidden := range []string{secret, pii} {
		if strings.Contains(output.String(), forbidden) {
			t.Errorf("JSON-предупреждение содержит исходную ошибку %q: %s", forbidden, output.String())
		}
	}
}

func TestAssessmentInvalidReassessmentDoesNotMutateValidAggregate(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID: "assessment-job", CompanyID: "assessment-company", Verdict: domain.VerdictFits,
		SignalKind: domain.SignalDirect,
		Evidence:   []domain.Evidence{{Criterion: "need", Fact: "fact", SourceURL: "https://example.test", Reasoning: "reason", SignalKind: domain.SignalDirect}},
		CreatedAt:  at,
	})
	if err != nil {
		t.Fatalf("создать допустимую оценку: %v", err)
	}
	before := assessment.Snapshot()
	err = assessment.ChangeVerdict(domain.VerdictFits, domain.SignalProfile, []domain.Evidence{{Missing: "missing"}}, at.Add(time.Minute))
	if !errors.Is(err, domain.ErrInvalidSignalKind) {
		t.Fatalf("недопустимая переоценка вернула %v, ожидалась ErrInvalidSignalKind", err)
	}
	if after := assessment.Snapshot(); !assessmentSnapshotsEqual(before, after) {
		t.Fatalf("неуспешная переоценка изменила агрегат: до=%+v после=%+v", before, after)
	}
	if events := assessment.Events(); len(events) != 1 {
		t.Fatalf("после отказа в переоценке накоплено %d событий, ожидалось только исходное LeadAssessed", len(events))
	}
}

func serviceDiagnosticsConfig() Config {
	return Config{
		Profiles: memory.NewLeadProfileRepository(), Contacts: memory.NewContactRepository(),
		Jobs: memory.NewSearchJobStore(), Messages: memory.NewOutboundMessageRepository(),
		Templates: memory.NewEmailTemplateRepository(), Activity: memory.NewLeadActivityStore(),
		Search: assembledSearchFailure{err: errors.New("unused")}, Crawl: assembledDiagnosticsCrawl{},
		Mail: noopMailPort{}, Logger: observ.NewLoggerWithWriter(&bytes.Buffer{}),
		Metrics: observ.NewMetrics(), Tracer: observ.NewTracer(ServiceID),
	}
}

type assembledAssessmentReadFailure struct {
	ports.LeadAssessmentStore
	err error
}

func (s assembledAssessmentReadFailure) GetByJobAndCompany(context.Context, string, string) (*domain.LeadAssessment, error) {
	return nil, s.err
}

type assembledAssessmentLLM struct{ llm.Port }

func (assembledAssessmentLLM) Assess(_ context.Context, req llm.AssessRequest) (llm.Assessment, llm.Usage, error) {
	return e2eFitsAssessment(e2eMaterialURL(req)), llm.Usage{}, nil
}

type errorArgLogger struct {
	observ.Logger
	errorArgs []error
}

func (l *errorArgLogger) Error(msg string, err error, fields observ.Fields, attrs ...any) {
	l.errorArgs = append(l.errorArgs, err)
	l.Logger.Error(msg, err, fields, attrs...)
}

func decodeJSONLogLines(t *testing.T, data string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("разобрать JSON-строку лога %q: %v", scanner.Text(), err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("прочитать JSON-логи: %v", err)
	}
	return entries
}

func assessmentSnapshotsEqual(a, b domain.LeadAssessmentSnapshot) bool {
	if a.JobID != b.JobID || a.CompanyID != b.CompanyID || a.Verdict != b.Verdict || a.SignalKind != b.SignalKind || !a.CreatedAt.Equal(b.CreatedAt) || !a.UpdatedAt.Equal(b.UpdatedAt) || len(a.Evidence) != len(b.Evidence) {
		return false
	}
	for i := range a.Evidence {
		if a.Evidence[i] != b.Evidence[i] {
			return false
		}
	}
	return true
}
