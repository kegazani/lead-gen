package app

import (
	"context"
	"io"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
)

// TestHandleCountsProgress проверяет накопление счётчиков прогресса: запланированные
// цели и URL, потраченные токены LLM, ошибки разбора и найденные компании.
func TestHandleCountsProgress(t *testing.T) {
	llmPort := &fakeLLM{
		targets: []llm.Target{{Query: "агентства b2b"}},
		usage:   llm.Usage{TotalTokens: 7},
	}
	// Обход отдаёт страницу без названия — разбор не удаётся, попытка не засчитывается.
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "текст без заголовка"}},
	}}
	fs := &fakeSearch{results: searchResult("https://a.example")}

	store := newMemJobs()
	r := &RunSearch{
		Jobs:     store,
		Profiles: newMemProfiles(),
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

	id := mustEnqueue(t, r, "агентства b2b")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"агентства b2b"}`)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	list, err := store.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	progress := list[0].Progress
	if progress == nil {
		t.Fatal("прогресс не записан")
	}
	if progress.PlannedTargets != 1 {
		t.Fatalf("PlannedTargets = %d, ожидалось 1", progress.PlannedTargets)
	}
	if progress.PlannedURLs != 1 {
		t.Fatalf("PlannedURLs = %d, ожидалось 1", progress.PlannedURLs)
	}
	if progress.TokensSpent != 7 {
		t.Fatalf("TokensSpent = %d, ожидалось 7", progress.TokensSpent)
	}
	if progress.ParseErrors != 1 {
		t.Fatalf("ParseErrors = %d, ожидалось 1", progress.ParseErrors)
	}
	if progress.CompaniesFound != 0 {
		t.Fatalf("CompaniesFound = %d, ожидалось 0", progress.CompaniesFound)
	}
}

// TestProgressCarriesVerdictDistribution проверяет, что прогресс несёт
// распределение оценок по вердиктам ОТДЕЛЬНО от числа страниц и найденных
// компаний и сохраняет его при публикации и на смене фазы.
func TestProgressCarriesVerdictDistribution(t *testing.T) {
	ctx := context.Background()
	store := newMemJobs()
	job, err := store.CreateSearchJob(ctx, "агентства b2b", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	rep := newProgressReporter(store, job.ID, func() time.Time { return testTime }, nil, nil, nil)
	rep.crawled(5)
	rep.found(3)
	rep.verdicts(ports.VerdictCounts{Fits: 1, Hypothesis: 1, Unsuitable: 1})
	rep.flush(ctx)

	progress := store.byID[job.ID].Progress
	if progress == nil {
		t.Fatal("прогресс не записан")
	}
	if progress.PagesCrawled != 5 || progress.CompaniesFound != 3 {
		t.Fatalf("страницы/компании = %d/%d, ожидалось 5/3", progress.PagesCrawled, progress.CompaniesFound)
	}
	if progress.VerdictFits != 1 || progress.VerdictHypothesis != 1 || progress.VerdictUnsuitable != 1 {
		t.Fatalf("распределение = %d/%d/%d, ожидалось 1/1/1",
			progress.VerdictFits, progress.VerdictHypothesis, progress.VerdictUnsuitable)
	}

	// Смена фазы публикует снимок — итоговое распределение сохраняется.
	rep.phase(ctx, PhaseFinalizing)
	progress = store.byID[job.ID].Progress
	if progress.VerdictFits != 1 || progress.VerdictHypothesis != 1 || progress.VerdictUnsuitable != 1 {
		t.Fatalf("распределение потеряно на смене фазы: %+v", progress)
	}
}

// TestProgressPersistsResultOnFlush проверяет, что репортер фиксирует снимок
// найденных компаний на границе flush, но только когда набор изменился: повторный
// flush с тем же набором не множит записи.
func TestProgressPersistsResultOnFlush(t *testing.T) {
	ctx := context.Background()
	store := newMemJobs()
	job, err := store.CreateSearchJob(ctx, "агентства b2b", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	var ids []string
	rep := newProgressReporter(store, job.ID, func() time.Time { return testTime }, nil, nil, nil)
	rep.result(func() []string { return ids })

	ids = []string{"a.example"}
	rep.flush(ctx)
	if got := store.byID[job.ID].ResultRef; len(got) != 1 || got[0] != "a.example" {
		t.Fatalf("ResultRef после первого flush = %v, ожидалось [a.example]", got)
	}
	if store.resultWrites != 1 {
		t.Fatalf("записей результата = %d, ожидалась 1", store.resultWrites)
	}

	// Тот же набор — записи нет.
	rep.flush(ctx)
	if store.resultWrites != 1 {
		t.Fatalf("повторный flush с тем же набором записал результат (вызовов %d)", store.resultWrites)
	}

	// Набор вырос — запись есть.
	ids = []string{"a.example", "b.example"}
	rep.flush(ctx)
	if got := store.byID[job.ID].ResultRef; len(got) != 2 || got[1] != "b.example" {
		t.Fatalf("ResultRef после роста набора = %v", got)
	}
	if store.resultWrites != 2 {
		t.Fatalf("записей результата = %d, ожидалось 2", store.resultWrites)
	}
}

func TestRunSearchRecoveryKeepsPriorResultsAndSkipsSatisfiedQuota(t *testing.T) {
	ctx := context.Background()
	store := newMemJobs()
	plan := ports.SearchPlan{Targets: []ports.PlanTarget{{Query: "unused recovery target"}}}
	job, err := store.CreateSearchJobWithPlan(ctx, "recovery query", plan, 0, 1, 0)
	if err != nil {
		t.Fatalf("CreateSearchJobWithPlan: %v", err)
	}
	if err := store.RecordResult(ctx, job.ID, []string{"prior.example"}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	searchPort := &fakeSearch{}
	crawlPort := &fakeCrawl{}
	run := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: searchPort, Crawl: crawlPort,
		Clock: fakeClock{t: testTime}, Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	if err := run.Handle(ctx, jobs.Job{ID: job.ID, Payload: store.payload[job.ID]}); err != nil {
		t.Fatalf("Handle recovered job: %v", err)
	}
	if len(searchPort.queries) != 0 || len(crawlPort.requests) != 0 {
		t.Fatalf("already satisfied recovery triggered search/crawl: %d/%d", len(searchPort.queries), len(crawlPort.requests))
	}
	recovered, err := store.GetSearchJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetSearchJob: %v", err)
	}
	if len(recovered.ResultRef) != 1 || recovered.ResultRef[0] != "prior.example" {
		t.Fatalf("recovery lost previous result refs: %v", recovered.ResultRef)
	}
}
