package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/search"
)

// errPlanUnavailable — ошибка планирования в тестах добора.
var errPlanUnavailable = errors.New("планирование недоступно")

// quotaPayload кодирует payload задания с планом, целевым числом компаний и
// лимитом результатов поиска.
func quotaPayload(t *testing.T, query string, targets []ports.PlanTarget, targetCompanies, maxResults, maxCredits int) []byte {
	t.Helper()
	plan := ports.SearchPlan{Targets: targets, Limits: ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}}
	raw, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{
		Query:           query,
		MaxCredits:      maxCredits,
		TargetCompanies: targetCompanies,
		MaxResults:      maxResults,
		Plan:            &plan,
	})
	if err != nil {
		t.Fatalf("EncodeSearchJobPayload: %v", err)
	}
	return raw
}

// quotaRunSearch собирает use case с фиксированным набором портов для тестов
// квоты и добора.
func quotaRunSearch(store *memJobs, profiles *memProfiles, fs *fakeSearch, fc *fakeCrawl, llmPort llm.Port) *RunSearch {
	return &RunSearch{
		Jobs:     store,
		Profiles: profiles,
		Contacts: newMemContacts(),
		Search:   fs,
		Crawl:    fc,
		LLM:      llmPort,
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}
}

// finalProgress возвращает итоговый снимок прогресса задания.
func finalProgress(t *testing.T, store *memJobs, id string) ports.SearchJobProgress {
	t.Helper()
	job := store.byID[id]
	if job == nil || job.Progress == nil {
		t.Fatal("итоговый прогресс не записан")
	}
	return *job.Progress
}

// TestHandleStopsAtTargetCompanies проверяет остановку задания по достижении
// целевого числа компаний: задание завершается done после N уникальных компаний и
// не обходит оставшиеся цели плана.
func TestHandleStopsAtTargetCompanies(t *testing.T) {
	fs := &fakeSearch{byQuery: map[string][]search.Result{
		"q1": searchResult("https://a.example"),
		"q2": searchResult("https://b.example"),
		"q3": searchResult("https://c.example"),
	}}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
		"https://b.example": {{URL: "https://b.example", Markdown: "# B Corp\n\nhello@b.example"}},
		"https://c.example": {{URL: "https://c.example", Markdown: "# C Corp\n\nhello@c.example"}},
	}}
	store := newMemJobs()
	profiles := newMemProfiles()
	r := quotaRunSearch(store, profiles, fs, fc, nil)

	id := mustEnqueue(t, r, "цель")
	payload := quotaPayload(t, "цель", []ports.PlanTarget{{Query: "q1"}, {Query: "q2"}, {Query: "q3"}}, 2, 0, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(fs.queries) != 2 {
		t.Fatalf("поиск вызван %d раз, ожидалось 2: третья цель не должна запускаться при достижении цели", len(fs.queries))
	}
	if len(fc.requests) != 2 {
		t.Fatalf("обход вызван %d раз, ожидалось 2", len(fc.requests))
	}
	p := finalProgress(t, store, id)
	if p.CompaniesFound != 2 || p.TargetCompanies != 2 {
		t.Fatalf("прогресс цели: найдено %d из %d, ожидалось 2 из 2", p.CompaniesFound, p.TargetCompanies)
	}
	if !p.TargetReached || p.StopReason != "" {
		t.Fatalf("цель достигнута, причина не должна фиксироваться: %+v", p)
	}
	if p.Phase != PhaseFinalizing {
		t.Fatalf("итоговая фаза = %q, ожидалась %q", p.Phase, PhaseFinalizing)
	}
	for _, snapshot := range store.progressHistory[id] {
		if snapshot.Phase == PhaseExpanding {
			t.Fatalf("добор не должен запускаться при достижении цели: %v", store.progressHistory[id])
		}
	}
}

// TestHandleSkipsAlreadyCrawledURL проверяет дедупликацию обойдённых URL: один и
// тот же адрес из разных целей обходится один раз.
func TestHandleSkipsAlreadyCrawledURL(t *testing.T) {
	fs := &fakeSearch{results: searchResult("https://a.example")}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
	}}
	store := newMemJobs()
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, nil)

	id := mustEnqueue(t, r, "дедуп")
	payload := quotaPayload(t, "дедуп", []ports.PlanTarget{{Query: "q1"}, {Query: "q2"}}, 0, 0, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(fc.requests) != 1 {
		t.Fatalf("повторный адрес обходится заново: вызовов %d, ожидался 1", len(fc.requests))
	}
	if p := finalProgress(t, store, id); p.CompaniesFound != 1 {
		t.Fatalf("найдено компаний %d, ожидалась 1", p.CompaniesFound)
	}
}

// TestHandlePassesMaxResults проверяет, что лимит результатов из payload доходит
// до поискового запроса.
func TestHandlePassesMaxResults(t *testing.T) {
	fs := &fakeSearch{results: searchResult("https://a.example")}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
	}}
	store := newMemJobs()
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, nil)

	id := mustEnqueue(t, r, "лимит")
	payload := quotaPayload(t, "лимит", []ports.PlanTarget{{Query: "q1"}}, 0, 15, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(fs.queries) != 1 || fs.queries[0].MaxResults != 15 {
		t.Fatalf("лимит результатов не дошёл до поиска: %+v", fs.queries)
	}
}

// TestHandleExpandsTargetsUntilGoal проверяет добор целей: при исчерпании целей и
// недостигнутой цели задание добирает новые цели через LLM, публикует фазу добора
// и продолжает обход, исключая уже использованные цели.
func TestHandleExpandsTargetsUntilGoal(t *testing.T) {
	fs := &fakeSearch{byQuery: map[string][]search.Result{
		"seed":  searchResult("https://a.example"),
		"more1": searchResult("https://b.example"),
		"more2": searchResult("https://c.example"),
	}}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
		"https://b.example": {{URL: "https://b.example", Markdown: "# B Corp\n\nhello@b.example"}},
		"https://c.example": {{URL: "https://c.example", Markdown: "# C Corp\n\nhello@c.example"}},
	}}
	llmPort := &fakeLLM{planQueue: [][]llm.Target{
		{{Query: "more1"}},
		{{Query: "more2"}},
	}}
	store := newMemJobs()
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, llmPort)

	id := mustEnqueue(t, r, "квота")
	payload := quotaPayload(t, "квота", []ports.PlanTarget{{Query: "seed"}}, 3, 0, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	p := finalProgress(t, store, id)
	if p.CompaniesFound != 3 || !p.TargetReached || p.StopReason != "" {
		t.Fatalf("добор не довёл задание до цели: %+v", p)
	}
	if llmPort.planCalls != 2 {
		t.Fatalf("Plan вызван %d раз, ожидалось 2 добора", llmPort.planCalls)
	}
	if !containsPhase(phaseHistory(store, id), PhaseExpanding) {
		t.Fatalf("фаза добора не опубликована: %v", phaseHistory(store, id))
	}
	// Перечень использованных целей уходит в запрос планирования: добор не должен
	// повторять уже задействованные цели.
	if len(llmPort.planRequests) == 0 || !strings.Contains(llmPort.planRequests[0].Query, "seed") {
		t.Fatalf("использованные цели не переданы в Plan: %+v", llmPort.planRequests)
	}
}

// TestHandleStopsRefineOnEmptyGrowth проверяет, что добор без прироста целей
// завершает задание done с явной причиной, а не зацикливается.
func TestHandleStopsRefineOnEmptyGrowth(t *testing.T) {
	fs := &fakeSearch{byQuery: map[string][]search.Result{
		"seed": searchResult("https://a.example"),
	}}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
	}}
	// Plan возвращает уже использованную цель: прироста нет.
	llmPort := &fakeLLM{planQueue: [][]llm.Target{{{Query: "seed"}}}}
	store := newMemJobs()
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, llmPort)

	id := mustEnqueue(t, r, "пусто")
	payload := quotaPayload(t, "пусто", []ports.PlanTarget{{Query: "seed"}}, 3, 0, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	p := finalProgress(t, store, id)
	if p.TargetReached {
		t.Fatalf("цель не должна считаться достигнутой: %+v", p)
	}
	if p.StopReason != stopReasonTargetsExhausted {
		t.Fatalf("причина остановки = %q, ожидалась %q", p.StopReason, stopReasonTargetsExhausted)
	}
	if llmPort.planCalls != 1 {
		t.Fatalf("Plan вызван %d раз, ожидался 1: пустой прирост должен остановить добор", llmPort.planCalls)
	}
}

// TestHandleStopsRefineWithoutLLM проверяет, что без LLM добор невозможен и
// задание останавливается с причиной «цели исчерпаны».
func TestHandleStopsRefineWithoutLLM(t *testing.T) {
	fs := &fakeSearch{byQuery: map[string][]search.Result{
		"seed": searchResult("https://a.example"),
	}}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
	}}
	store := newMemJobs()
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, nil)

	id := mustEnqueue(t, r, "без llm")
	payload := quotaPayload(t, "без llm", []ports.PlanTarget{{Query: "seed"}}, 3, 0, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	p := finalProgress(t, store, id)
	if p.StopReason != stopReasonTargetsExhausted {
		t.Fatalf("причина остановки = %q, ожидалась %q", p.StopReason, stopReasonTargetsExhausted)
	}
}

// TestHandleStopsRefineOnExhaustedBudget проверяет проверку бюджета перед добором:
// при исчерпанном бюджете добор не запускается, задание завершается с причиной
// «бюджет исчерпан».
func TestHandleStopsRefineOnExhaustedBudget(t *testing.T) {
	fs := &fakeSearch{byQuery: map[string][]search.Result{
		"seed":  searchResult("https://a.example"),
		"more1": searchResult("https://b.example"),
	}}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
		"https://b.example": {{URL: "https://b.example", Markdown: "# B Corp\n\nhello@b.example"}},
	}}
	llmPort := &fakeLLM{planQueue: [][]llm.Target{{{Query: "more1"}}}}
	store := newMemJobs()
	// available=0 при заданном бюджете: меньше минимальной стоимости следующего шага.
	factory := &fakeBudgetFactory{search: fs, crawl: fc, available: 0}
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, llmPort)
	r.Budget = factory

	id := mustEnqueue(t, r, "бюджет")
	payload := quotaPayload(t, "бюджет", []ports.PlanTarget{{Query: "seed"}}, 3, 0, 10)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	p := finalProgress(t, store, id)
	if p.StopReason != stopReasonBudgetExhausted {
		t.Fatalf("причина остановки = %q, ожидалась %q", p.StopReason, stopReasonBudgetExhausted)
	}
	if llmPort.planCalls != 0 {
		t.Fatalf("Plan вызван %d раз: при исчерпанном бюджете добор не должен запускаться", llmPort.planCalls)
	}
}

// TestHandleStopsRefineOnPlanError проверяет, что ошибка Plan останавливает добор,
// но не роняет задание: найденное сохраняется, причина фиксируется.
func TestHandleStopsRefineOnPlanError(t *testing.T) {
	fs := &fakeSearch{byQuery: map[string][]search.Result{
		"seed": searchResult("https://a.example"),
	}}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
	}}
	llmPort := &fakeLLM{err: errPlanUnavailable}
	store := newMemJobs()
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, llmPort)

	id := mustEnqueue(t, r, "ошибка")
	payload := quotaPayload(t, "ошибка", []ports.PlanTarget{{Query: "seed"}}, 3, 0, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	p := finalProgress(t, store, id)
	if p.StopReason != stopReasonRefineFailed {
		t.Fatalf("причина остановки = %q, ожидалась %q", p.StopReason, stopReasonRefineFailed)
	}
	if p.CompaniesFound != 1 {
		t.Fatalf("найденные до остановки компании потеряны: %+v", p)
	}
}

// TestHandleStopsRefineOnIterationLimit проверяет, что добор ограничен пределом
// итераций: даже при приросте целей цикл завершается, а не идёт бесконечно.
func TestHandleStopsRefineOnIterationLimit(t *testing.T) {
	fs := &fakeSearch{byQuery: map[string][]search.Result{}}
	fc := &fakeCrawl{}
	llmPort := &fakeLLM{planQueue: [][]llm.Target{
		{{Query: "more1"}},
		{{Query: "more2"}},
		{{Query: "more3"}},
	}}
	store := newMemJobs()
	r := quotaRunSearch(store, newMemProfiles(), fs, fc, llmPort)
	r.RefineIterations = 1

	id := mustEnqueue(t, r, "предел")
	payload := quotaPayload(t, "предел", []ports.PlanTarget{{Query: "seed"}}, 3, 0, 0)
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	p := finalProgress(t, store, id)
	if p.StopReason != stopReasonRefineLimit {
		t.Fatalf("причина остановки = %q, ожидалась %q", p.StopReason, stopReasonRefineLimit)
	}
	if llmPort.planCalls != 1 {
		t.Fatalf("Plan вызван %d раз, ожидался 1 (предел итераций)", llmPort.planCalls)
	}
}

// phaseHistory возвращает последовательность фаз задания.
func phaseHistory(store *memJobs, id string) []string {
	phases := make([]string, 0, len(store.progressHistory[id]))
	for _, snapshot := range store.progressHistory[id] {
		phases = append(phases, snapshot.Phase)
	}
	return phases
}
