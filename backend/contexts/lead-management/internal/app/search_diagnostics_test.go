package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/search"
)

type loggerErrorSpy struct {
	observ.Logger
	gotErrors []error
}

func (l *loggerErrorSpy) Error(message string, err error, fields observ.Fields, attrs ...any) {
	l.gotErrors = append(l.gotErrors, err)
	l.Logger.Error(message, err, fields, attrs...)
}

type failingProfileRepo struct {
	ports.LeadProfileRepository
	err error
}

func (r failingProfileRepo) FindByID(context.Context, domain.CompanyID) (*domain.Company, error) {
	return nil, r.err
}

func TestRunSearchHandleLogsOnlyNormalizedFatalFailures(t *testing.T) {
	const (
		secret  = "sentinel-secret-7d31"
		pii     = "person.sentinel@example.test"
		query   = "query-sentinel " + secret
		pageURL = "https://" + secret + ".example/private/" + pii
	)
	baseErr := fmt.Errorf("provider failed: %s %s %s", secret, pii, pageURL)
	cases := []struct {
		name string
		err  error
		code string
	}{
		{name: "обычная", err: baseErr},
		{name: "обёрнутая", err: fmt.Errorf("wrapped: %w", baseErr)},
		{name: "coded", err: newSearchJobError(ErrorCodeCrawlFailed, baseErr), code: string(ErrorCodeCrawlFailed)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			spy := &loggerErrorSpy{Logger: observ.NewLoggerWithWriter(&log)}
			metrics := observ.NewMetrics().(*observ.MemoryMetrics)
			store := newMemJobs()
			jobID := "diagnostic-job-" + tc.name
			r := &RunSearch{
				Jobs: store, Profiles: failingProfileRepo{err: tc.err}, Contacts: newMemContacts(),
				Search: &fakeSearch{results: searchResult(pageURL)},
				Crawl:  &fakeCrawl{pages: map[string][]crawl.Page{pageURL: {{URL: pageURL, Markdown: "# Safe name\n\nDescription"}}}},
				Logger: spy, Metrics: metrics, Tracer: observ.NewTracer("lead-management"),
				FailureNormalizer: NewSearchFailureNormalizer(), ServiceName: "lead-management",
			}
			if err := store.Create(context.Background(), jobs.Job{ID: jobID}); err != nil {
				t.Fatalf("создать запись задания: %v", err)
			}
			payload := []byte(fmt.Sprintf(`{"query":%q}`, query))
			gotErr := r.Handle(context.Background(), jobs.Job{ID: jobID, Payload: payload})
			if !errors.Is(gotErr, tc.err) {
				t.Fatalf("возвращённая ошибка %v не сохраняет исходную цепочку %v", gotErr, tc.err)
			}
			if len(spy.gotErrors) != 1 || spy.gotErrors[0] != nil {
				t.Fatalf("Logger.Error args = %v, ожидался единственный nil", spy.gotErrors)
			}
			logged := log.String()
			for _, forbidden := range []string{secret, pii, query, pageURL, "domain", "company"} {
				if strings.Contains(logged, forbidden) {
					t.Errorf("лог содержит запрещённое значение %q: %s", forbidden, logged)
				}
			}
			for _, safe := range []string{`"error_code":`, `"reason":`, `"diagnostic":`, `"task_id":"` + jobID + `"`} {
				if !strings.Contains(logged, safe) {
					t.Errorf("в логе нет безопасного поля %s: %s", safe, logged)
				}
			}
			if tc.code != "" && !strings.Contains(logged, `"error_code":"`+tc.code+`"`) {
				t.Errorf("код отказа в логе не сохранён: %s", logged)
			}
			if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "failed"}); got != 1 {
				t.Errorf("jobs_total failed = %d, ожидался 1", got)
			}
			var errorEvents int
			for _, event := range store.events {
				if event.Type == ports.EventError {
					errorEvents++
					if event.Code != tc.code {
						t.Errorf("EventError.Code = %q, ожидался %q", event.Code, tc.code)
					}
				}
			}
			wantEvents := 0
			if tc.code != "" {
				wantEvents = 1
			}
			if errorEvents != wantEvents {
				t.Errorf("EventError count = %d, ожидалось %d", errorEvents, wantEvents)
			}
		})
	}
}

func TestRunSearchHandleNilNormalizerUsesSafeFallback(t *testing.T) {
	var log bytes.Buffer
	spy := &loggerErrorSpy{Logger: observ.NewLoggerWithWriter(&log)}
	store := newMemJobs()
	const secret = "fallback-secret user@example.test"
	r := &RunSearch{
		Jobs: store, Profiles: failingProfileRepo{err: errors.New(secret)}, Contacts: newMemContacts(),
		Search: &fakeSearch{results: searchResult("https://safe.example")},
		Crawl:  &fakeCrawl{pages: map[string][]crawl.Page{"https://safe.example": {{URL: "https://safe.example", Markdown: "# Safe\n\nDescription"}}}},
		Logger: spy, Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}
	if err := store.Create(context.Background(), jobs.Job{ID: "fallback-job"}); err != nil {
		t.Fatalf("создать запись задания: %v", err)
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: "fallback-job", Payload: []byte(`{"query":"safe query"}`)}); err == nil {
		t.Fatal("ожидалась ошибка записи карточки")
	}
	if strings.Contains(log.String(), secret) {
		t.Fatalf("fallback допустил исходный текст в лог: %s", log.String())
	}
	if len(spy.gotErrors) != 1 || spy.gotErrors[0] != nil {
		t.Fatalf("Logger.Error не получил nil: %v", spy.gotErrors)
	}
	if !strings.Contains(log.String(), `"error_code":"job.failed"`) || !strings.Contains(log.String(), `"diagnostic":"unknown"`) {
		t.Fatalf("безопасный fallback не записан: %s", log.String())
	}
}

func TestNormalizeSearchFailureUsesInjectedFiniteCatalog(t *testing.T) {
	normalizer := jobs.NewCatalogFailureNormalizer(map[string]jobs.FailureRule{
		"custom.safe": {Message: "Безопасная формулировка", Diagnostic: "custom_diagnostic"},
	}, jobs.Failure{Code: "job.failed", Message: "Ошибка задания", Diagnostic: "unknown"})
	got := normalizeSearchFailure(normalizer, codedError{code: "custom.safe", message: "SECRET"})
	if got != (jobs.Failure{Code: "custom.safe", Message: "Безопасная формулировка", Diagnostic: "custom_diagnostic"}) {
		t.Fatalf("Normalize = %+v", got)
	}
}

type retryResultStore struct {
	*memJobs
	remaining int
	called    int
	err       error
}

func (s *retryResultStore) RecordResult(ctx context.Context, id string, refs []string) error {
	s.called++
	if s.remaining > 0 {
		s.remaining--
		return s.err
	}
	return s.memJobs.RecordResult(ctx, id, refs)
}

func TestProgressReporterUsesSafeResultDiagnosticAndRetries(t *testing.T) {
	const raw = "result-secret person@example.test"
	var output bytes.Buffer
	store := &retryResultStore{memJobs: newMemJobs(), remaining: 1, err: errors.New(raw)}
	job, err := store.CreateSearchJob(context.Background(), "safe query", 0)
	if err != nil {
		t.Fatalf("создать задание: %v", err)
	}
	normalizer := &identityFailureNormalizer{}
	rep := newProgressReporter(store, job.ID, func() time.Time { return testTime }, nil, observ.NewLoggerWithWriter(&output), normalizer)
	if rep.failureNormalizer != normalizer {
		t.Fatal("progressReporter получил другой FailureNormalizer")
	}
	rep.result(func() []string { return []string{"safe.example"} })
	rep.flush(context.Background())
	if rep.resultError() == nil {
		t.Fatal("после неуспешной записи результат должен оставаться незаписанным")
	}
	if !strings.Contains(output.String(), `"diagnostic_code":"result_record_failed"`) || strings.Contains(output.String(), raw) {
		t.Fatalf("лог сбоя результата небезопасен или не классифицирован: %s", output.String())
	}
	rep.flush(context.Background())
	rep.flush(context.Background())
	if store.called != 2 || store.resultWrites != 1 {
		t.Fatalf("RecordResult вызван %d раз, успешных записей %d; ожидались retry ×1 и одна запись", store.called, store.resultWrites)
	}
	if rep.resultError() != nil {
		t.Fatalf("успешный повтор оставил ошибку результата: %v", rep.resultError())
	}
}

func TestRunSearchFailsWhenDurableResultCannotBeSaved(t *testing.T) {
	const (
		query = "найти логистические компании"
		url   = "https://result-write-failed.example/about"
	)
	store := &retryResultStore{memJobs: newMemJobs(), remaining: 100, err: errors.New("result store unavailable")}
	run := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: &fakeSearch{results: []search.Result{{URL: url}}},
		Crawl: &fakeCrawl{pages: map[string][]crawl.Page{url: {{
			URL:      url,
			Markdown: "# Result Write Co\n\nописание компании",
			Metadata: map[string]string{"title": "Result Write Co", "description": "описание компании"},
		}}}},
		Logger: observ.NewLoggerWithWriter(&bytes.Buffer{}), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
		Limits: testCrawlLimits(),
	}
	jobID := mustEnqueue(t, run, query)
	err := run.Handle(context.Background(), jobs.Job{ID: jobID, Payload: mustPayload(t, query, 0)})
	var searchErr *SearchJobError
	if !errors.As(err, &searchErr) || searchErr.Code() != string(ErrorCodeResultSaveFailed) {
		t.Fatalf("Handle error = %v, ожидается result save failure", err)
	}
}

func TestRunSearchHandleNonfatalFailuresUseFiniteDiagnosticCodes(t *testing.T) {
	const (
		query = "private query sentinel@example.test"
		url   = "https://private.example/path/sentinel"
		raw   = "provider-secret person@example.test"
	)
	cases := []struct {
		name   string
		code   string
		search *fakeSearch
		crawl  *fakeCrawl
	}{
		{name: "поиск", code: "search_request_failed", search: &fakeSearch{err: errors.New(raw)}, crawl: &fakeCrawl{}},
		{name: "обход", code: "crawl_request_failed", search: &fakeSearch{results: searchResult(url)}, crawl: &fakeCrawl{crawlErr: errors.New(raw)}},
		{name: "извлечение", code: "extract_request_failed", search: &fakeSearch{results: searchResult(url)}, crawl: &fakeCrawl{
			pages: map[string][]crawl.Page{url: {{URL: url, Markdown: "# Incomplete"}}}, extractErr: errors.New(raw),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			r := &RunSearch{
				Jobs: newMemJobs(), Profiles: newMemProfiles(), Contacts: newMemContacts(),
				Search: tc.search, Crawl: tc.crawl,
				Logger: observ.NewLoggerWithWriter(&output), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
				ServiceName: "lead-management",
			}
			id := mustEnqueue(t, r, query)
			if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: mustPayload(t, query, 0)}); err != nil {
				t.Fatalf("нефатальный сбой не должен останавливать задание: %v", err)
			}
			logged := output.String()
			if !strings.Contains(logged, `"diagnostic_code":"`+tc.code+`"`) {
				t.Errorf("нет ожидаемого diagnostic_code %q: %s", tc.code, logged)
			}
			for _, forbidden := range []string{raw, query, url, "private.example"} {
				if strings.Contains(logged, forbidden) {
					t.Errorf("лог содержит запрещённое значение %q: %s", forbidden, logged)
				}
			}
		})
	}
}

func TestSearchDiagnosticsUsePassedNormalizerInstance(t *testing.T) {
	normalizer := &identityFailureNormalizer{}
	reporter := newProgressReporter(newMemJobs(), "task-1", func() time.Time { return testTime }, nil, nil, normalizer)
	runner := &RunSearch{FailureNormalizer: normalizer}
	if runner.FailureNormalizer != reporter.failureNormalizer {
		t.Fatal("RunSearch и progressReporter используют разные экземпляры нормализатора")
	}
}

type diagnosticAssessmentStore struct {
	ports.LeadAssessmentStore
	getErr  error
	saveErr error
}

func (s diagnosticAssessmentStore) GetByJobAndCompany(context.Context, string, string) (*domain.LeadAssessment, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return nil, ports.ErrAssessmentNotFound
}

func (s diagnosticAssessmentStore) Save(context.Context, *domain.LeadAssessment) error {
	return s.saveErr
}

type budgetRecordFailStore struct {
	*memJobs
	err error
}

func (s budgetRecordFailStore) RecordBudget(context.Context, string, budget.Snapshot) error {
	return s.err
}

func TestRunSearchHandleAssessmentReadAndSaveDiagnostics(t *testing.T) {
	const raw = "assessment-secret person@example.test"
	cases := []struct {
		name       string
		store      diagnosticAssessmentStore
		diagnostic string
	}{
		{name: "read", store: diagnosticAssessmentStore{getErr: errors.New(raw)}, diagnostic: "assessment_read_failed"},
		{name: "save", store: diagnosticAssessmentStore{saveErr: errors.New(raw)}, diagnostic: "assessment_save_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			url := "https://safe.example"
			r := &RunSearch{
				Jobs: newMemJobs(), Profiles: newMemProfiles(), Contacts: newMemContacts(),
				Search: &fakeSearch{results: searchResult(url)},
				Crawl:  &fakeCrawl{pages: map[string][]crawl.Page{url: {{URL: url, Markdown: "# Safe\n\nDescription"}}}},
				LLM:    &fakeLLM{targets: []llm.Target{{Query: "safe query"}}}, Assessments: tc.store,
				Logger: observ.NewLoggerWithWriter(&output), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
			}
			id := mustEnqueue(t, r, "safe query")
			if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: mustPayload(t, "safe query", 0)}); err != nil {
				t.Fatalf("нефатальная ошибка оценки не должна падать из Handle: %v", err)
			}
			if !strings.Contains(output.String(), `"diagnostic_code":"`+tc.diagnostic+`"`) || strings.Contains(output.String(), raw) {
				t.Fatalf("лог ошибки оценки небезопасен или не классифицирован: %s", output.String())
			}
		})
	}
}

func TestRunSearchHandleAssessmentComputeErrorIsNonfatalAndSafe(t *testing.T) {
	const (
		secret = "llm-assessment-secret-5fa1"
		pii    = "assessment.person@example.test"
		query  = "private assessment query"
		url    = "https://assessment-private.example/path"
	)
	var output bytes.Buffer
	logger := &loggerErrorSpy{Logger: observ.NewLoggerWithWriter(&output)}
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	normalizer := &countingFailureNormalizer{}
	r := &RunSearch{
		Jobs: newMemJobs(), Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: &fakeSearch{results: searchResult(url)},
		Crawl: &fakeCrawl{pages: map[string][]crawl.Page{url: {{
			URL: url, Metadata: map[string]string{"title": "Safe company", "description": "Safe description"},
			Markdown: "# Safe company\n\nSafe description\n\ncontact@safe.example",
		}}}},
		LLM:         &fakeLLM{err: fmt.Errorf("LLM failed: %s %s", secret, pii)},
		Assessments: diagnosticAssessmentStore{}, Logger: logger, Metrics: metrics,
		Tracer: observ.NewTracer("lead-management"), FailureNormalizer: normalizer, ServiceName: "lead-management",
	}
	jobID := mustEnqueue(t, r, query)
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{
		Query: query,
		Plan:  &ports.SearchPlan{Targets: []ports.PlanTarget{{Query: "safe target"}}},
	})
	if err != nil {
		t.Fatalf("собрать payload: %v", err)
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: jobID, Payload: payload}); err != nil {
		t.Fatalf("ошибка оценки LLM не должна прерывать поиск: %v", err)
	}
	if normalizer.calls != 0 {
		t.Fatalf("фиксированная диагностика этапа неожиданно вызвала FailureNormalizer %d раз", normalizer.calls)
	}
	if len(logger.gotErrors) != 1 || logger.gotErrors[0] != nil {
		t.Fatalf("Logger.Error args = %v, ожидался ровно один nil для безопасной диагностики", logger.gotErrors)
	}
	logged := output.String()
	if !strings.Contains(logged, `"diagnostic_code":"assessment_compute_failed"`) || !strings.Contains(logged, `"task_id":"`+jobID+`"`) {
		t.Fatalf("нет безопасной диагностики сбоя вычисления оценки: %s", logged)
	}
	for _, forbidden := range []string{secret, pii, query, url, "assessment-private.example"} {
		if strings.Contains(logged, forbidden) {
			t.Errorf("лог содержит исходное значение %q: %s", forbidden, logged)
		}
	}
	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "done"}); got != 1 {
		t.Errorf("jobs_total done = %d, ожидался 1", got)
	}
	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "failed"}); got != 0 {
		t.Errorf("нефатальный сбой оценки учтён как отказ: jobs_total failed = %d", got)
	}
}

func TestRunSearchHandleCancellationDuringCrawlReturnsCancelled(t *testing.T) {
	const query = "cancel while crawling"
	ctx, cancel := context.WithCancel(context.Background())
	crawlPort := &waitingForCancellationCrawl{started: make(chan struct{})}
	logger, metrics, tracer := testObservability()
	store := newMemJobs()
	r := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: &fakeSearch{results: searchResult("https://cancel.example")}, Crawl: crawlPort,
		Logger: logger, Metrics: metrics, Tracer: tracer, ServiceName: "lead-management",
	}
	jobID := mustEnqueue(t, r, query)
	payload := mustPayload(t, query, 0)
	result := make(chan error, 1)
	go func() { result <- r.Handle(ctx, jobs.Job{ID: jobID, Payload: payload}) }()
	select {
	case <-crawlPort.started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("Handle не дошёл до Crawl, ожидающего отмену")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, jobs.ErrCancelled) {
			t.Fatalf("Handle после отмены вернул %v, ожидалась jobs.ErrCancelled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Handle не завершился после отмены контекста")
	}
	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "cancelled"}); got != 1 {
		t.Errorf("jobs_total cancelled = %d, ожидался 1", got)
	}
	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "failed"}); got != 0 {
		t.Errorf("отмена была учтена как failed: %d", got)
	}
	for _, event := range store.events {
		if event.Type == ports.EventError {
			t.Errorf("отмена создала EventError: %+v", event)
		}
	}
}

type waitingForCancellationCrawl struct{ started chan struct{} }

func (c *waitingForCancellationCrawl) Crawl(ctx context.Context, _ crawl.Request) (crawl.CrawlOutcome, error) {
	close(c.started)
	<-ctx.Done()
	return crawl.CrawlOutcome{}, crawl.ErrCancelled
}

func (*waitingForCancellationCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

func TestLogAssessmentWarnUsesFixedRevisionDiagnostic(t *testing.T) {
	const inaccessibleSentinel = "revision-secret-not-an-argument person@example.test"
	var output bytes.Buffer
	logger := &loggerErrorSpy{Logger: observ.NewLoggerWithWriter(&output)}
	r := &RunSearch{Logger: logger}
	// Ошибка намеренно не передаётся helper'у: его контракт принимает только
	// безопасные метки этапа, поэтому исходный текст недоступен для сериализации.
	_ = errors.New(inaccessibleSentinel)
	r.logAssessmentWarn("assessment-task-safe", "ревизия оценки компании", "assessment_revision_failed")
	logged := output.String()
	if !strings.Contains(logged, `"diagnostic_code":"assessment_revision_failed"`) ||
		!strings.Contains(logged, `"task_id":"assessment-task-safe"`) {
		t.Fatalf("нет фиксированного JSON-предупреждения ревизии: %s", logged)
	}
	if strings.Contains(logged, inaccessibleSentinel) {
		t.Fatalf("в журнал попала недоступная helper'у ошибка: %s", logged)
	}
	if len(logger.gotErrors) != 0 {
		t.Fatalf("предупреждение неожиданно вызвало Logger.Error: %v", logger.gotErrors)
	}
}

type countingFailureNormalizer struct{ calls int }

func (n *countingFailureNormalizer) Normalize(error) jobs.Failure {
	n.calls++
	return jobs.Failure{Code: "counted.failure", Message: "Безопасно", Diagnostic: "counted"}
}

func TestRunSearchHandleBudgetRecordDiagnostic(t *testing.T) {
	const raw = "budget-secret person@example.test"
	var output bytes.Buffer
	store := budgetRecordFailStore{memJobs: newMemJobs(), err: errors.New(raw)}
	url := "https://safe.example"
	searchPort := &fakeSearch{results: searchResult(url)}
	crawlPort := &fakeCrawl{pages: map[string][]crawl.Page{url: {{URL: url, Markdown: "# Safe\n\nDescription"}}}}
	r := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(), Search: searchPort, Crawl: crawlPort,
		Budget: &fakeBudgetFactory{search: searchPort, crawl: crawlPort},
		Logger: observ.NewLoggerWithWriter(&output), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}
	id := mustEnqueue(t, r, "safe query")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: mustPayload(t, "safe query", 0)}); err != nil {
		t.Fatalf("сбой записи бюджета не должен менять успешный исход: %v", err)
	}
	if !strings.Contains(output.String(), `"diagnostic_code":"budget_record_failed"`) || strings.Contains(output.String(), raw) {
		t.Fatalf("лог сбоя записи бюджета небезопасен или не классифицирован: %s", output.String())
	}
}

type codedError struct{ code, message string }

func (e codedError) Error() string   { return e.message }
func (e codedError) Code() string    { return e.code }
func (e codedError) Message() string { return e.message }

var _ search.Port = (*fakeSearch)(nil)

type identityFailureNormalizer struct{}

func (*identityFailureNormalizer) Normalize(error) jobs.Failure {
	return jobs.Failure{Code: "job.failed", Message: "Ошибка задания", Diagnostic: "unknown"}
}
