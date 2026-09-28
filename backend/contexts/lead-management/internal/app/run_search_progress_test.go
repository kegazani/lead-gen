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

	rep := newProgressReporter(store, job.ID, func() time.Time { return testTime }, nil)
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
