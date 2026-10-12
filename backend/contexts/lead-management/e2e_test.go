package leadmanagement

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/search"
)

// fixedClock — детерминированные часы: всегда возвращают зафиксированный момент,
// чтобы даты в карточках и заданиях не зависели от системного времени.
type fixedClock struct {
	now time.Time
}

// Now возвращает зафиксированный момент.
func (c fixedClock) Now() time.Time { return c.now }

// fakeSearch — фейк порта поиска, возвращающий один зафиксированный результат.
type fakeSearch struct {
	results []search.Result
}

// Search возвращает зафиксированные результаты, игнорируя запрос.
func (f *fakeSearch) Search(_ context.Context, _ search.Query) ([]search.Result, error) {
	return f.results, nil
}

// fakeSearch реализует порт поиска.
var _ search.Port = (*fakeSearch)(nil)

// fakeCrawl — фейк порта обхода: отдаёт одну зафиксированную страницу и не
// поддерживает эскалацию на структурированную экстракцию.
type fakeCrawl struct {
	pages []crawl.Page
}

// Crawl возвращает зафиксированные страницы, игнорируя параметры обхода.
func (f *fakeCrawl) Crawl(_ context.Context, _ crawl.Request) (crawl.CrawlOutcome, error) {
	return crawl.CrawlOutcome{Pages: f.pages}, nil
}

// Extract в этом сценарии вызываться не должен: дешёвые слои экстракции дают
// полную карточку. Любой вызов возвращает ошибку, чтобы эскалация провалила тест
// громко, а не молча.
func (f *fakeCrawl) Extract(_ context.Context, _ crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, errors.New("эскалация на crawl.Extract не должна запускаться")
}

// fakeCrawl реализует порт обхода.
var _ crawl.Port = (*fakeCrawl)(nil)

// TestSearchToConsoleEndToEnd проверяет сквозной сценарий «запрос → поиск →
// обход → карточки → таблица в консоли» на тестовых адаптерах (фейки портов и
// in-memory хранилища) без обращения к внешним службам.
func TestSearchToConsoleEndToEnd(t *testing.T) {
	ctx := context.Background()

	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	profiles := memory.NewLeadProfileRepository()
	contacts := memory.NewContactRepository()
	jobsStore := memory.NewSearchJobStore()

	searchPort := &fakeSearch{results: []search.Result{{URL: "https://example.com"}}}
	crawlPort := &fakeCrawl{pages: []crawl.Page{{
		URL:      "https://example.com",
		Markdown: "# Example Corp\ncontact@example.com\nhttps://linkedin.com/example",
		Metadata: map[string]string{"title": "Example Corp"},
	}}}

	logger := observ.NewLoggerWithWriter(io.Discard)
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	tracer := observ.NewTracer("lead-management")

	runSearch := &app.RunSearch{
		Jobs:        jobsStore,
		Profiles:    profiles,
		Contacts:    contacts,
		Search:      searchPort,
		Crawl:       crawlPort,
		LLM:         nil, // без LLM-планирования
		Clock:       clock,
		Limits:      app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
		Logger:      logger,
		Metrics:     metrics,
		Tracer:      tracer,
		ServiceName: "lead-management",
	}
	showcase := &app.Showcase{
		Jobs:     jobsStore,
		Profiles: profiles,
		Contacts: contacts,
		Messages: memory.NewOutboundMessageRepository(),
	}

	// 1. Постановка задания поиска (роль api).
	job, err := runSearch.Enqueue(ctx, "example corp", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job.Status != jobs.StatusQueued {
		t.Fatalf("статус после Enqueue = %q, ожидался queued", job.Status)
	}

	// 2. Один проход раннера заданий (роль worker).
	runner := &jobs.Runner{Store: jobsStore, Handler: runSearch.Handle}
	processed, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("RunOnce обработал %d заданий, ожидалось 1", processed)
	}

	// Задание должно перейти в done.
	searchJobs, err := jobsStore.ListSearchJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(searchJobs) != 1 {
		t.Fatalf("в журнале %d заданий, ожидалось 1", len(searchJobs))
	}
	if searchJobs[0].Status != jobs.StatusDone {
		t.Fatalf("статус задания = %q, ожидался done", searchJobs[0].Status)
	}
	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "done"}); got != 1 {
		t.Fatalf("jobs_total done = %d, ожидался 1", got)
	}

	// 3. Таблица карточек и карточка в консоли (роль api).
	server := httpapi.NewServer(httpapi.Dependencies{
		RunSearch: runSearch,
		Showcase:  showcase,
		Logger:    logger,
		Auth:      httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
	ts := httptest.NewServer(server)
	defer ts.Close()

	// Вход оператора: cookie сессии сохраняется в jar и подставляется в запросы.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginResp, err := client.PostForm(ts.URL+"/login", url.Values{
		"username": {"operator"},
		"password": {"secret"},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	loginResp.Body.Close()

	resp, err := client.Get(ts.URL + "/cards")
	if err != nil {
		t.Fatalf("GET /cards: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cards = %d, ожидался 200", resp.StatusCode)
	}
	if html := readBody(t, resp); !strings.Contains(html, "Example Corp") {
		t.Fatalf("таблица карточек не содержит «Example Corp»:\n%s", html)
	}

	resp, err = client.Get(ts.URL + "/cards/example.com")
	if err != nil {
		t.Fatalf("GET /cards/example.com: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cards/example.com = %d, ожидался 200", resp.StatusCode)
	}
	if html := readBody(t, resp); !strings.Contains(html, "contact@example.com") {
		t.Fatalf("карточка не содержит «contact@example.com»:\n%s", html)
	}
}

// readBody читает и закрывает тело ответа, возвращая его строкой.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	return string(data)
}

// TestConsoleRequestObservability проверяет, что входящий запрос консоли через
// observ.Middleware порождает лог с correlation_id и метрики запроса.
func TestConsoleRequestObservability(t *testing.T) {
	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	tracer := observ.NewTracer("lead-management")

	server := httpapi.NewServer(httpapi.Dependencies{
		Logger: logger,
		Auth:   httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
	handler := observ.Middleware(logger, metrics, tracer, "lead-management")(server)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := metrics.Counter("http_requests_total", map[string]string{"service": "lead-management", "code": "200"}); got != 1 {
		t.Fatalf("http_requests_total = %d, ожидался 1", got)
	}
	if !strings.Contains(buf.String(), `"correlation_id":"`) {
		t.Fatalf("лог запроса не содержит correlation_id: %s", buf.String())
	}
}

// blockingCrawl — фейк обхода, который ждёт отмены контекста и отдаёт страницу
// вместе с crawl.ErrCancelled (как адаптер Firecrawl при отмене).
type blockingCrawl struct {
	started chan struct{}
	page    crawl.Page
}

func (f *blockingCrawl) Crawl(ctx context.Context, _ crawl.Request) (crawl.CrawlOutcome, error) {
	close(f.started)
	<-ctx.Done()
	return crawl.CrawlOutcome{Pages: []crawl.Page{f.page}}, crawl.ErrCancelled
}

func (f *blockingCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// TestCancelRunningJobEndToEnd проверяет сквозной сценарий отмены: бюджет задан на
// запрос, отмена выполняющегося обхода сохраняет уже добытые карточки, задание
// завершается статусом cancelled, а прогресс отражает ход.
func TestCancelRunningJobEndToEnd(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	profiles := memory.NewLeadProfileRepository()
	contacts := memory.NewContactRepository()
	jobsStore := memory.NewSearchJobStore()
	signals := jobs.NewCancelRegistry()

	searchPort := &fakeSearch{results: []search.Result{{URL: "https://example.com"}}}
	crawlPort := &blockingCrawl{
		started: make(chan struct{}),
		page: crawl.Page{
			URL:      "https://example.com",
			Markdown: "# Example Corp\ncontact@example.com",
			Metadata: map[string]string{"title": "Example Corp"},
		},
	}

	runSearch := &app.RunSearch{
		Jobs: jobsStore, Profiles: profiles, Contacts: contacts,
		Search: searchPort, Crawl: crawlPort, Signals: signals,
		Clock:       clock,
		Limits:      app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
		Logger:      observ.NewLoggerWithWriter(io.Discard),
		Metrics:     observ.NewMetrics(),
		Tracer:      observ.NewTracer("lead-management"),
		ServiceName: "lead-management",
	}

	job, err := runSearch.Enqueue(ctx, "example corp", 10)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runner := &jobs.Runner{Store: jobsStore, Handler: runSearch.Handle, Signals: signals}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runner.RunOnce(ctx); err != nil {
			t.Errorf("RunOnce: %v", err)
		}
	}()

	<-crawlPort.started
	if err := runSearch.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	<-done

	list, err := jobsStore.ListSearchJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("в журнале %d заданий, ожидалось 1", len(list))
	}
	entry := list[0]
	if entry.Status != jobs.StatusCancelled {
		t.Fatalf("статус задания = %q, ожидался cancelled", entry.Status)
	}
	if entry.MaxCredits != 10 {
		t.Fatalf("бюджет задания = %d, ожидалось 10", entry.MaxCredits)
	}
	if entry.Progress == nil || entry.Progress.CompaniesFound != 1 {
		t.Fatalf("прогресс не отражает найденную компанию: %+v", entry.Progress)
	}
	if len(entry.ResultRef) != 1 || entry.ResultRef[0] != "example.com" {
		t.Fatalf("отменённое задание должно нести ссылки на найденные компании: %v", entry.ResultRef)
	}

	showcase := &app.Showcase{Profiles: profiles, Contacts: memory.NewContactRepository()}
	cards, err := showcase.ListCards(ctx, 10)
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	found := false
	for _, c := range cards {
		if c.ID == "example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("карточка из отменённого обхода не сохранена: %+v", cards)
	}
}

// TestCancelRunningJobViaStoreWithoutSignal проверяет сценарий раздельных
// процессов: отмена фиксируется только в хранилище (сигнал в процесс-обработчик
// не доставляется), но задание всё равно останавливается на границе шага.
func TestCancelRunningJobViaStoreWithoutSignal(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	profiles := memory.NewLeadProfileRepository()
	contacts := memory.NewContactRepository()
	jobsStore := memory.NewSearchJobStore()

	crawlPort := &blockingCrawl{
		started: make(chan struct{}),
		page: crawl.Page{
			URL:      "https://example.com",
			Markdown: "# Example Corp\ncontact@example.com",
			Metadata: map[string]string{"title": "Example Corp"},
		},
	}
	runSearch := &app.RunSearch{
		Jobs: jobsStore, Profiles: profiles, Contacts: contacts,
		Search: &fakeSearch{results: []search.Result{{URL: "https://example.com"}}}, Crawl: crawlPort,
		Clock:       clock,
		Limits:      app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
		Logger:      observ.NewLoggerWithWriter(io.Discard),
		Metrics:     observ.NewMetrics(),
		Tracer:      observ.NewTracer("lead-management"),
		ServiceName: "lead-management",
	}

	job, err := runSearch.Enqueue(ctx, "example corp", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Signals не задан: единственный канал отмены — данные хранилища.
	runner := &jobs.Runner{Store: jobsStore, Handler: runSearch.Handle, Interval: 10 * time.Millisecond}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runner.RunOnce(ctx); err != nil {
			t.Errorf("RunOnce: %v", err)
		}
	}()

	<-crawlPort.started
	// Отмена «из другого процесса»: только запись в хранилище.
	if err := jobsStore.RequestCancel(ctx, job.ID); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	<-done

	list, err := jobsStore.ListSearchJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(list) != 1 || list[0].Status != jobs.StatusCancelled {
		t.Fatalf("статус задания = %+v, ожидался cancelled", list)
	}
}

// e2eSearch — фейк порта поиска для сквозных сценариев оценки: результаты
// задаются по тексту поискового запроса.
type e2eSearch struct {
	byQuery map[string][]search.Result
}

// Search отдаёт результаты, привязанные к тексту запроса.
func (f *e2eSearch) Search(_ context.Context, q search.Query) ([]search.Result, error) {
	return f.byQuery[q.Text], nil
}

// e2eSearch реализует порт поиска.
var _ search.Port = (*e2eSearch)(nil)

// e2eCrawl — фейк порта обхода для сквозных сценариев: на каждый стартовый URL
// отдаётся зафиксированная страница, вызовы запоминаются для проверки лимитов.
type e2eCrawl struct {
	byURL    map[string]crawl.Page
	requests []crawl.Request
}

// Crawl отдаёт страницу, привязанную к стартовому URL (пустой исход — пропуск).
func (f *e2eCrawl) Crawl(_ context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	f.requests = append(f.requests, req)
	page, ok := f.byURL[req.StartURL]
	if !ok {
		return crawl.CrawlOutcome{}, nil
	}
	return crawl.CrawlOutcome{Pages: []crawl.Page{page}}, nil
}

// Extract в этих сценариях вызываться не должен: дешёвые слои дают полную
// карточку. Любой вызов возвращает ошибку, чтобы эскалация провалила тест громко.
func (f *e2eCrawl) Extract(_ context.Context, _ crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, errors.New("эскалация на crawl.Extract не должна запускаться")
}

// e2eCrawl реализует порт обхода.
var _ crawl.Port = (*e2eCrawl)(nil)

// e2eAssessLLM — фейк llm.Port для сквозных сценариев: интерпретация и вердикт
// задаются тестом, вызовы Assess запоминаются.
type e2eAssessLLM struct {
	interpret   func(llm.InterpretRequest) llm.Interpretation
	assess      func(llm.AssessRequest) llm.Assessment
	assessCalls []llm.AssessRequest
}

// Plan отдаёт исходный запрос единственной целью планирования.
func (f *e2eAssessLLM) Plan(_ context.Context, req llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	return []llm.Target{{Query: req.Query}}, llm.Usage{}, nil
}

// Suggest в этих сценариях не используется.
func (f *e2eAssessLLM) Suggest(context.Context, llm.SuggestRequest) ([]llm.Suggestion, llm.Usage, error) {
	return nil, llm.Usage{}, nil
}

// Converse в этих сценариях не используется.
func (f *e2eAssessLLM) Converse(context.Context, llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	return llm.ConverseReply{}, llm.Usage{}, nil
}

// ConverseStream в этих сценариях не используется.
func (f *e2eAssessLLM) ConverseStream(context.Context, llm.ConverseRequest) iter.Seq2[llm.ConverseDelta, error] {
	return func(func(llm.ConverseDelta, error) bool) {}
}

// Interpret возвращает критерии, заданные тестом.
func (f *e2eAssessLLM) Interpret(_ context.Context, req llm.InterpretRequest) (llm.Interpretation, llm.Usage, error) {
	if f.interpret == nil {
		return llm.Interpretation{}, llm.Usage{}, nil
	}
	return f.interpret(req), llm.Usage{}, nil
}

// Assess возвращает вердикт, вычисленный функцией сценария.
func (f *e2eAssessLLM) Assess(_ context.Context, req llm.AssessRequest) (llm.Assessment, llm.Usage, error) {
	f.assessCalls = append(f.assessCalls, req)
	return f.assess(req), llm.Usage{}, nil
}

// e2eAssessLLM реализует порт LLM.
var _ llm.Port = (*e2eAssessLLM)(nil)

// e2ePage собирает страницу, из которой дешёвые слои экстракции дают полную
// карточку: название и описание из метаданных, email из markdown.
func e2ePage(url, name, description, email string) crawl.Page {
	return crawl.Page{
		URL:      url,
		Metadata: map[string]string{"title": name, "description": description},
		Markdown: "# " + name + "\n\n" + description + "\n\ncontact: " + email,
	}
}

// e2eFitsAssessment собирает вердикт «Подходит» с прямым заявлением и ссылкой на
// источник, подтверждающий факт.
func e2eFitsAssessment(sourceURL string) llm.Assessment {
	return llm.Assessment{
		Verdict:    llm.VerdictFits,
		SignalKind: llm.SignalDirect,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "ищет автоматизацию",
			Fact:       "компания прямо заявила потребность",
			SourceURL:  sourceURL,
			Reasoning:  "заявление совпадает с критерием запроса",
			SignalKind: llm.SignalDirect,
		}},
	}
}

// e2eHypothesisAssessment собирает вердикт «Гипотеза»: профиль совпал, сигнала
// потребности нет — вердикт не «Подходит».
func e2eHypothesisAssessment(sourceURL string) llm.Assessment {
	return llm.Assessment{
		Verdict:    llm.VerdictHypothesis,
		SignalKind: llm.SignalProfile,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "ищет автоматизацию",
			Fact:       "компания совпала по профилю",
			SourceURL:  sourceURL,
			Reasoning:  "профиль совпадает, потребность не заявлена",
			SignalKind: llm.SignalProfile,
			Missing:    "нет наблюдаемого признака потребности",
		}},
	}
}

// e2eUnsuitableAssessment собирает вердикт «Не подходит» с установленным
// несоответствием запросу.
func e2eUnsuitableAssessment(sourceURL string) llm.Assessment {
	return llm.Assessment{
		Verdict:    llm.VerdictUnsuitable,
		SignalKind: llm.SignalProfile,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "ищет автоматизацию",
			Fact:       "компания явно не соответствует типу клиента",
			SourceURL:  sourceURL,
			Reasoning:  "обязательное ограничение запроса нарушено",
			SignalKind: llm.SignalProfile,
			Mismatch:   "несоответствие: компания вне целевого типа клиента",
		}},
	}
}

// e2eMaterialURL возвращает URL первого материала оценки (страницы обхода).
func e2eMaterialURL(req llm.AssessRequest) string {
	if len(req.Material) == 0 {
		return ""
	}
	return req.Material[0].URL
}

// e2eEnv — окружение сквозного сценария оценки на in-memory хранилищах.
type e2eEnv struct {
	jobs        *memory.SearchJobStore
	profiles    *memory.LeadProfileRepository
	contacts    *memory.ContactRepository
	assessments *memory.AssessmentStore
	runSearch   *app.RunSearch
}

// newE2EEnv собирает use case поиска с оценкой на in-memory хранилищах.
func newE2EEnv(searchPort search.Port, crawlPort crawl.Port, llmPort llm.Port, clock ports.Clock) *e2eEnv {
	env := &e2eEnv{
		jobs:        memory.NewSearchJobStore(),
		profiles:    memory.NewLeadProfileRepository(),
		contacts:    memory.NewContactRepository(),
		assessments: memory.NewAssessmentStore(),
	}
	env.runSearch = &app.RunSearch{
		Jobs: env.jobs, Profiles: env.profiles, Contacts: env.contacts,
		Search: searchPort, Crawl: crawlPort, LLM: llmPort, Assessments: env.assessments,
		Clock:   clock,
		Limits:  app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
		ServiceName: "lead-management",
	}
	return env
}

// process прогоняет один проход раннера заданий.
func (e *e2eEnv) process(t *testing.T) {
	t.Helper()
	runner := &jobs.Runner{Store: e.jobs, Handler: e.runSearch.Handle}
	processed, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("RunOnce обработал %d заданий, ожидалось 1", processed)
	}
}

// console поднимает консоль поверх use case'ов окружения и логинит оператора.
func (e *e2eEnv) console(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	showcase := &app.Showcase{
		Jobs: e.jobs, Profiles: e.profiles, Contacts: e.contacts,
		Messages: memory.NewOutboundMessageRepository(),
	}
	server := httpapi.NewServer(httpapi.Dependencies{
		RunSearch: e.runSearch,
		Showcase:  showcase,
		Logger:    observ.NewLoggerWithWriter(io.Discard),
		Auth:      httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return ts, e2eLogin(t, ts)
}

// e2eJobByID возвращает задание из журнала по идентификатору.
func e2eJobByID(t *testing.T, store *memory.SearchJobStore, id string) ports.SearchJob {
	t.Helper()
	list, err := store.ListSearchJobs(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	for _, j := range list {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("задание %s не найдено в журнале", id)
	return ports.SearchJob{}
}

// e2eLogin выполняет вход оператора и возвращает клиент с cookie сессии.
func e2eLogin(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(ts.URL+"/login", url.Values{"username": {"operator"}, "password": {"secret"}})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	resp.Body.Close()
	return client
}

// e2eGet выполняет GET и возвращает статус вместе с телом ответа.
func e2eGet(t *testing.T, client *http.Client, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := client.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode, readBody(t, resp)
}

// e2eRequireContains проверяет, что разметка несёт все ожидаемые фрагменты.
func e2eRequireContains(t *testing.T, html string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(html, want) {
			t.Fatalf("разметка не содержит %q:\n%s", want, html)
		}
	}
}

// TestLeadRelevanceEndToEnd проверяет сквозной путь оценки: запрос → интерпретация
// → поиск → обход → карточка компании → оценка релевантности, сохранённая
// относительно задания. Распределение вердиктов и основания «Подходит» со ссылкой
// на источник видны на экране задания, а список карточек в контексте задания несёт
// «Подходящие» по умолчанию и вторую группу («Гипотеза»/«Не подходит»).
func TestLeadRelevanceEndToEnd(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	crawlPort := &e2eCrawl{byURL: map[string]crawl.Page{
		"https://fit.example":  e2ePage("https://fit.example", "Fit Corp", "Прямо заявили потребность", "hello@fit.example"),
		"https://hypo.example": e2ePage("https://hypo.example", "Hypo Corp", "Только профиль", "hello@hypo.example"),
		"https://no.example":   e2ePage("https://no.example", "No Corp", "Явно не наш клиент", "hello@no.example"),
	}}
	searchPort := &e2eSearch{byQuery: map[string][]search.Result{
		"b2b агентства": {
			{URL: "https://fit.example"},
			{URL: "https://hypo.example"},
			{URL: "https://no.example"},
		},
	}}
	llmPort := &e2eAssessLLM{
		interpret: func(llm.InterpretRequest) llm.Interpretation {
			return llm.Interpretation{TargetLead: "B2B-агентство", NeedSignals: []string{"ищет автоматизацию"}}
		},
		assess: func(req llm.AssessRequest) llm.Assessment {
			source := e2eMaterialURL(req)
			switch {
			case strings.Contains(source, "fit.example"):
				return e2eFitsAssessment(source)
			case strings.Contains(source, "hypo.example"):
				return e2eHypothesisAssessment(source)
			default:
				return e2eUnsuitableAssessment(source)
			}
		},
	}

	env := newE2EEnv(searchPort, crawlPort, llmPort, clock)
	job, err := env.runSearch.EnqueueInterpreted(ctx, "b2b агентства", nil, ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueInterpreted: %v", err)
	}
	env.process(t)

	// Оценки привязаны к заданию: вердикт и свидетельства со ссылкой на источник.
	fits, err := env.assessments.GetByJobAndCompany(ctx, job.ID, "fit.example")
	if err != nil {
		t.Fatalf("оценка fit.example не сохранена: %v", err)
	}
	if fits.Verdict() != domain.VerdictFits {
		t.Fatalf("fit.example: вердикт = %q, ожидался fits", fits.Verdict())
	}
	if ev := fits.Evidence(); len(ev) == 0 || ev[0].SourceURL != "https://fit.example" {
		t.Fatalf("свидетельство «Подходит» без ссылки на источник: %+v", ev)
	}

	hypo, err := env.assessments.GetByJobAndCompany(ctx, job.ID, "hypo.example")
	if err != nil {
		t.Fatalf("оценка hypo.example не сохранена: %v", err)
	}
	if hypo.Verdict() != domain.VerdictHypothesis {
		t.Fatalf("hypo.example: вердикт = %q, ожидался hypothesis", hypo.Verdict())
	}
	if ev := hypo.Evidence(); len(ev) == 0 || strings.TrimSpace(ev[0].Missing) == "" {
		t.Fatalf("гипотеза без недостающего свидетельства: %+v", ev)
	}

	unsuitable, err := env.assessments.GetByJobAndCompany(ctx, job.ID, "no.example")
	if err != nil {
		t.Fatalf("оценка no.example не сохранена: %v", err)
	}
	if unsuitable.Verdict() != domain.VerdictUnsuitable {
		t.Fatalf("no.example: вердикт = %q, ожидался unsuitable", unsuitable.Verdict())
	}
	if ev := unsuitable.Evidence(); len(ev) == 0 || strings.TrimSpace(ev[0].Mismatch) == "" {
		t.Fatalf("«Не подходит» без установленного несоответствия: %+v", ev)
	}

	// Прогресс задания несёт распределение оценок отдельно от страниц и компаний.
	entry := e2eJobByID(t, env.jobs, job.ID)
	if entry.Status != jobs.StatusDone {
		t.Fatalf("статус задания = %q, ожидался done", entry.Status)
	}
	if entry.Progress == nil {
		t.Fatal("прогресс задания не опубликован")
	}
	if entry.Progress.VerdictFits != 1 || entry.Progress.VerdictHypothesis != 1 || entry.Progress.VerdictUnsuitable != 1 {
		t.Fatalf("распределение оценок в прогрессе = %+v, ожидалось 1/1/1", entry.Progress)
	}
	if entry.Progress.CompaniesFound != 3 || entry.Progress.PagesCrawled != 3 || entry.Progress.ParseErrors != 0 {
		t.Fatalf("счётчики страниц/компаний/ошибок = %+v", entry.Progress)
	}
	dist, err := env.assessments.VerdictDistribution(ctx, job.ID)
	if err != nil {
		t.Fatalf("VerdictDistribution: %v", err)
	}
	if dist.Fits != 1 || dist.Hypothesis != 1 || dist.Unsuitable != 1 {
		t.Fatalf("распределение оценок хранилища = %+v, ожидалось 1/1/1", dist)
	}

	// Экран задания: распределение вердиктов и основания «Подходит» с источником.
	ts, client := env.console(t)
	status, jobHTML := e2eGet(t, client, ts, "/jobs/"+url.PathEscape(job.ID))
	if status != http.StatusOK {
		t.Fatalf("GET /jobs/%s = %d, ожидался 200", job.ID, status)
	}
	e2eRequireContains(t, jobHTML, []string{
		`data-verdict="fits">1<`,
		`data-verdict="hypothesis">1<`,
		`data-verdict="unsuitable">1<`,
		"Подходящие: основания и источники",
		"https://fit.example",
		"прямое заявление",
	})

	// Список карточек в контексте задания: «Подходящие» по умолчанию, вторая
	// группа — «Гипотеза» и «Не подходит»; обе группы остаются в разметке.
	status, cardsHTML := e2eGet(t, client, ts, "/cards?job="+url.QueryEscape(job.ID))
	if status != http.StatusOK {
		t.Fatalf("GET /cards?job=%s = %d, ожидался 200", job.ID, status)
	}
	e2eRequireContains(t, cardsHTML, []string{
		`data-cards-composition="fits"`,
		`data-cards-view="tiles"`,
		`data-composition-set="hypothesis"`,
		`data-view-set="rows"`,
		`href="/cards/fit.example" data-group="fits"`,
		`href="/cards/hypo.example" data-group="hypothesis"`,
		`href="/cards/no.example" data-group="unsuitable"`,
		"verdict-unsuitable",
		"Гипотеза",
		"Не подходит",
	})
}

// TestAssessmentNotTransferredBetweenJobsEndToEnd проверяет жёсткий запрет
// переноса: одна и та же компания участвует в двух заданиях с разными критериями
// и получает независимые оценки. Экран каждого задания показывает только свою
// оценку, чужая не подставляется.
func TestAssessmentNotTransferredBetweenJobsEndToEnd(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	crawlPort := &e2eCrawl{byURL: map[string]crawl.Page{
		"https://shared.example": e2ePage("https://shared.example", "Shared Corp", "Общая компания", "hello@shared.example"),
	}}
	searchPort := &e2eSearch{byQuery: map[string][]search.Result{
		"критерий A": {{URL: "https://shared.example"}},
		"критерий B": {{URL: "https://shared.example"}},
	}}
	llmPort := &e2eAssessLLM{
		interpret: func(req llm.InterpretRequest) llm.Interpretation {
			if req.Query == "критерий A" {
				return llm.Interpretation{TargetLead: "A", NeedSignals: []string{"сигнал A"}}
			}
			return llm.Interpretation{TargetLead: "B", NeedSignals: []string{"сигнал B"}}
		},
		assess: func(req llm.AssessRequest) llm.Assessment {
			source := e2eMaterialURL(req)
			if req.Interpretation.TargetLead == "A" {
				return e2eFitsAssessment(source)
			}
			return e2eHypothesisAssessment(source)
		},
	}

	env := newE2EEnv(searchPort, crawlPort, llmPort, clock)

	jobA, err := env.runSearch.EnqueueInterpreted(ctx, "критерий A", nil, ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueInterpreted A: %v", err)
	}
	env.process(t)

	jobB, err := env.runSearch.EnqueueInterpreted(ctx, "критерий B", nil, ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueInterpreted B: %v", err)
	}
	env.process(t)

	assessmentA, err := env.assessments.GetByJobAndCompany(ctx, jobA.ID, "shared.example")
	if err != nil {
		t.Fatalf("оценка задания A не сохранена: %v", err)
	}
	assessmentB, err := env.assessments.GetByJobAndCompany(ctx, jobB.ID, "shared.example")
	if err != nil {
		t.Fatalf("оценка задания B не сохранена: %v", err)
	}
	if assessmentA.Verdict() != domain.VerdictFits {
		t.Fatalf("задание A: вердикт = %q, ожидался fits", assessmentA.Verdict())
	}
	if assessmentB.Verdict() != domain.VerdictHypothesis {
		t.Fatalf("задание B: вердикт = %q (перенос из A?), ожидался hypothesis", assessmentB.Verdict())
	}

	ts, client := env.console(t)

	statusA, htmlA := e2eGet(t, client, ts, "/jobs/"+url.PathEscape(jobA.ID))
	if statusA != http.StatusOK {
		t.Fatalf("GET /jobs A = %d, ожидался 200", statusA)
	}
	if !strings.Contains(htmlA, "verdict-fits") || strings.Contains(htmlA, "verdict-hypothesis") {
		t.Fatalf("экран задания A показал чужую/не свою оценку:\n%s", htmlA)
	}

	statusB, htmlB := e2eGet(t, client, ts, "/jobs/"+url.PathEscape(jobB.ID))
	if statusB != http.StatusOK {
		t.Fatalf("GET /jobs B = %d, ожидался 200", statusB)
	}
	if !strings.Contains(htmlB, "verdict-hypothesis") || strings.Contains(htmlB, "verdict-fits") {
		t.Fatalf("экран задания B показал чужую/не свою оценку:\n%s", htmlB)
	}

	// Карточка компании в контексте задания показывает вердикт именно этого задания.
	_, cardA := e2eGet(t, client, ts, "/cards/shared.example?job="+url.QueryEscape(jobA.ID))
	if !strings.Contains(cardA, "verdict-fits") || strings.Contains(cardA, "verdict-hypothesis") {
		t.Fatalf("карточка в задании A показала не свою оценку:\n%s", cardA)
	}
	_, cardB := e2eGet(t, client, ts, "/cards/shared.example?job="+url.QueryEscape(jobB.ID))
	if !strings.Contains(cardB, "verdict-hypothesis") || strings.Contains(cardB, "verdict-fits") {
		t.Fatalf("карточка в задании B показала не свою оценку:\n%s", cardB)
	}
}

// TestParseErrorIsNotLeadAssessmentEndToEnd проверяет различение ситуаций:
// страница без разбора даёт ParseErrors и НЕ создаёт оценку, а «сигнал не найден»
// даёт «Гипотезу», а не «Подходит».
func TestParseErrorIsNotLeadAssessmentEndToEnd(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	// Задание 1: страница без названия и домена — ошибка разбора, не оценка.
	brokenCrawl := &e2eCrawl{byURL: map[string]crawl.Page{
		"https://broken.example": {},
	}}
	brokenSearch := &e2eSearch{byQuery: map[string][]search.Result{
		"битый сайт": {{URL: "https://broken.example"}},
	}}
	brokenLLM := &e2eAssessLLM{
		interpret: func(llm.InterpretRequest) llm.Interpretation {
			return llm.Interpretation{TargetLead: "B2B"}
		},
		assess: func(req llm.AssessRequest) llm.Assessment {
			return e2eFitsAssessment(e2eMaterialURL(req))
		},
	}
	brokenEnv := newE2EEnv(brokenSearch, brokenCrawl, brokenLLM, clock)
	brokenJob, err := brokenEnv.runSearch.EnqueueInterpreted(ctx, "битый сайт", nil, ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueInterpreted: %v", err)
	}
	brokenEnv.process(t)

	list, err := brokenEnv.assessments.ListByJob(ctx, brokenJob.ID)
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ошибка разбора не должна создавать оценку: %+v", list)
	}
	if len(brokenLLM.assessCalls) != 0 {
		t.Fatalf("оценка вызвана при ошибке разбора: %d вызовов", len(brokenLLM.assessCalls))
	}
	brokenEntry := e2eJobByID(t, brokenEnv.jobs, brokenJob.ID)
	if brokenEntry.Progress == nil || brokenEntry.Progress.ParseErrors != 1 {
		t.Fatalf("ошибка разбора не зафиксирована в прогрессе: %+v", brokenEntry.Progress)
	}
	if brokenEntry.Progress.CompaniesFound != 0 || len(brokenEntry.ResultRef) != 0 {
		t.Fatalf("ошибочная страница не должна засчитываться компанией: %+v", brokenEntry)
	}

	// Задание 2: сигнал не найден — «Гипотеза», а не «Подходит».
	hypoCrawl := &e2eCrawl{byURL: map[string]crawl.Page{
		"https://profile.example": e2ePage("https://profile.example", "Profile Corp", "Просто профиль", "hello@profile.example"),
	}}
	hypoSearch := &e2eSearch{byQuery: map[string][]search.Result{
		"профиль": {{URL: "https://profile.example"}},
	}}
	hypoLLM := &e2eAssessLLM{
		interpret: func(llm.InterpretRequest) llm.Interpretation {
			return llm.Interpretation{TargetLead: "B2B"}
		},
		assess: func(req llm.AssessRequest) llm.Assessment {
			return e2eHypothesisAssessment(e2eMaterialURL(req))
		},
	}
	hypoEnv := newE2EEnv(hypoSearch, hypoCrawl, hypoLLM, clock)
	hypoJob, err := hypoEnv.runSearch.EnqueueInterpreted(ctx, "профиль", nil, ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueInterpreted: %v", err)
	}
	hypoEnv.process(t)

	assessment, err := hypoEnv.assessments.GetByJobAndCompany(ctx, hypoJob.ID, "profile.example")
	if err != nil {
		t.Fatalf("оценка не сохранена: %v", err)
	}
	if assessment.Verdict() == domain.VerdictFits {
		t.Fatalf("отсутствие сигнала дало «Подходит»: %q", assessment.Verdict())
	}
	if assessment.Verdict() != domain.VerdictHypothesis {
		t.Fatalf("вердикт = %q, ожидался hypothesis", assessment.Verdict())
	}
}

// TestShortSessionProgressEndToEnd проверяет короткую проверочную сессию: задание
// создаётся с явными глубиной/временем/бюджетом, обход несёт эти лимиты и
// внутренний предохранитель страниц, а прогресс отражает распределение оценок по
// вердиктам отдельно от страниц и компаний и сохраняется после завершения.
func TestShortSessionProgressEndToEnd(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	crawlPort := &e2eCrawl{byURL: map[string]crawl.Page{
		"https://fit.example":  e2ePage("https://fit.example", "Fit Corp", "Прямо заявили потребность", "hello@fit.example"),
		"https://hypo.example": e2ePage("https://hypo.example", "Hypo Corp", "Только профиль", "hello@hypo.example"),
	}}
	searchPort := &e2eSearch{byQuery: map[string][]search.Result{
		"короткая сессия": {
			{URL: "https://fit.example"},
			{URL: "https://hypo.example"},
		},
	}}
	llmPort := &e2eAssessLLM{
		interpret: func(llm.InterpretRequest) llm.Interpretation {
			return llm.Interpretation{TargetLead: "B2B", NeedSignals: []string{"ищет автоматизацию"}}
		},
		assess: func(req llm.AssessRequest) llm.Assessment {
			source := e2eMaterialURL(req)
			if strings.Contains(source, "fit.example") {
				return e2eFitsAssessment(source)
			}
			return e2eHypothesisAssessment(source)
		},
	}

	env := newE2EEnv(searchPort, crawlPort, llmPort, clock)
	job, err := env.runSearch.EnqueueShortSession(ctx, "короткая сессия", nil, ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 90}, 7, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueShortSession: %v", err)
	}
	if job.Status != jobs.StatusQueued {
		t.Fatalf("статус сессии после постановки = %q, ожидался queued", job.Status)
	}
	env.process(t)

	// Обход несёт явные лимиты сессии и внутренний предохранитель страниц.
	if len(crawlPort.requests) == 0 {
		t.Fatal("обход не запускался")
	}
	for _, req := range crawlPort.requests {
		if req.MaxDepth != 2 || req.MaxTimeSeconds != 90 {
			t.Fatalf("лимиты обхода = %+v, ожидались max_depth=2 max_time_seconds=90", req)
		}
		if req.MaxPages != 25 {
			t.Fatalf("внутренний предохранитель страниц = %d, ожидался 25", req.MaxPages)
		}
	}

	entry := e2eJobByID(t, env.jobs, job.ID)
	if entry.Status != jobs.StatusDone {
		t.Fatalf("статус сессии после завершения = %q, ожидался done", entry.Status)
	}
	if entry.MaxCredits != 7 {
		t.Fatalf("бюджет сессии = %d, ожидался 7", entry.MaxCredits)
	}
	if entry.Progress == nil {
		t.Fatal("прогресс сессии не опубликован")
	}
	// Распределение оценок отдельно от счётчиков страниц и компаний.
	if entry.Progress.VerdictFits != 1 || entry.Progress.VerdictHypothesis != 1 || entry.Progress.VerdictUnsuitable != 0 {
		t.Fatalf("распределение оценок сессии = %+v, ожидалось 1/1/0", entry.Progress)
	}
	if entry.Progress.CompaniesFound != 2 || entry.Progress.PagesCrawled != 2 {
		t.Fatalf("счётчики страниц/компаний сессии = %+v", entry.Progress)
	}
	// Распределение сохраняется после завершения и доступно из хранилища оценок.
	dist, err := env.assessments.VerdictDistribution(ctx, job.ID)
	if err != nil {
		t.Fatalf("VerdictDistribution: %v", err)
	}
	progressCounts := ports.VerdictCounts{
		Fits:       entry.Progress.VerdictFits,
		Hypothesis: entry.Progress.VerdictHypothesis,
		Unsuitable: entry.Progress.VerdictUnsuitable,
	}
	if dist != progressCounts {
		t.Fatalf("распределение хранилища %+v не совпало с прогрессом %+v", dist, progressCounts)
	}
}

// e2eMultiTargetLLM — фейк LLM с несколькими целями планирования: базовое
// поведение e2eAssessLLM, но Plan отдаёт заданный набор целей.
type e2eMultiTargetLLM struct {
	e2eAssessLLM
	targets []llm.Target
}

// Plan отдаёт заданные цели.
func (f *e2eMultiTargetLLM) Plan(context.Context, llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	return f.targets, llm.Usage{}, nil
}

// e2eSequentialCrawl — фейк обхода из двух целей: обход первого URL отдаёт
// страницу, обход второго сообщает о начале и ждёт освобождения, чтобы тест успел
// заглянуть на экран выполняющегося задания.
type e2eSequentialCrawl struct {
	byURL      map[string]crawl.Page
	blockURL   string
	once       sync.Once
	secondSeen chan struct{}
	release    chan struct{}
}

// Crawl отдаёт страницу для первого URL и блокируется на втором до release.
func (f *e2eSequentialCrawl) Crawl(ctx context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	if req.StartURL == f.blockURL {
		f.once.Do(func() { close(f.secondSeen) })
		select {
		case <-f.release:
			return crawl.CrawlOutcome{}, nil
		case <-ctx.Done():
			return crawl.CrawlOutcome{}, ctx.Err()
		}
	}
	page, ok := f.byURL[req.StartURL]
	if !ok {
		return crawl.CrawlOutcome{}, nil
	}
	return crawl.CrawlOutcome{Pages: []crawl.Page{page}}, nil
}

// Extract в этих сценариях не используется.
func (f *e2eSequentialCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, errors.New("эскалация на crawl.Extract не должна запускаться")
}

// TestGlobalCardsCompositionEndToEnd проверяет сквозной путь до общей витрины: без
// контекста задания карточки делятся по оценкам, полученным в задании, —
// «Подходит» в группе по умолчанию, «Не подходит» во второй.
func TestGlobalCardsCompositionEndToEnd(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	searchPort := &e2eSearch{byQuery: map[string][]search.Result{
		"запрос": {{URL: "https://alpha.example"}, {URL: "https://beta.example"}},
	}}
	crawlPort := &e2eCrawl{byURL: map[string]crawl.Page{
		"https://alpha.example": e2ePage("https://alpha.example", "Alpha Corp", "Описание alpha", "alpha@alpha.example"),
		"https://beta.example":  e2ePage("https://beta.example", "Beta Corp", "Описание beta", "beta@beta.example"),
	}}
	llmPort := &e2eAssessLLM{
		assess: func(req llm.AssessRequest) llm.Assessment {
			if strings.Contains(e2eMaterialURL(req), "alpha.example") {
				return e2eFitsAssessment(e2eMaterialURL(req))
			}
			return e2eUnsuitableAssessment(e2eMaterialURL(req))
		},
	}

	env := newE2EEnv(searchPort, crawlPort, llmPort, clock)
	if _, err := env.runSearch.Enqueue(ctx, "запрос", 0); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	env.process(t)

	ts, client := env.console(t)
	status, html := e2eGet(t, client, ts, "/cards")
	if status != http.StatusOK {
		t.Fatalf("GET /cards = %d, ожидался 200", status)
	}
	e2eRequireContains(t, html, []string{
		"data-group=\"fits\"",
		"data-group=\"unsuitable\"",
		"verdict-unsuitable",
		"Не подходит",
		"alpha.example",
		"beta.example",
	})
}

// TestRunningJobScreenShowsExtractedEndToEnd проверяет, что экран выполняющегося
// задания несёт уже извлечённые компании с email и опрос без ручной перезагрузки.
func TestRunningJobScreenShowsExtractedEndToEnd(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}

	searchPort := &e2eSearch{byQuery: map[string][]search.Result{
		"one": {{URL: "https://one.example"}},
		"two": {{URL: "https://two.example"}},
	}}
	crawlPort := &e2eSequentialCrawl{
		byURL: map[string]crawl.Page{
			"https://one.example": e2ePage("https://one.example", "One Corp", "Описание one", "one@one.example"),
		},
		blockURL:   "https://two.example",
		secondSeen: make(chan struct{}),
		release:    make(chan struct{}),
	}
	llmPort := &e2eMultiTargetLLM{targets: []llm.Target{{Query: "one"}, {Query: "two"}}}
	llmPort.assess = func(req llm.AssessRequest) llm.Assessment { return e2eFitsAssessment(e2eMaterialURL(req)) }

	env := newE2EEnv(searchPort, crawlPort, llmPort, clock)
	job, err := env.runSearch.Enqueue(ctx, "запрос", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	ts, client := env.console(t)

	runner := &jobs.Runner{Store: env.jobs, Handler: env.runSearch.Handle}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runner.RunOnce(ctx)
	}()

	select {
	case <-crawlPort.secondSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("обход второй цели не начался")
	}

	status, html := e2eGet(t, client, ts, "/jobs/"+url.PathEscape(job.ID))
	if status != http.StatusOK {
		t.Fatalf("GET /jobs/{id} = %d, ожидался 200", status)
	}
	e2eRequireContains(t, html, []string{
		"one.example",
		"one@one.example",
		"hx-get=\"/jobs/" + job.ID + "\"",
		"every 2s",
	})

	close(crawlPort.release)
	<-done
}
