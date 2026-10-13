package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/search"
)

func TestRunSearchPlanWithoutLLM(t *testing.T) {
	r := &RunSearch{}
	targets, tokens := r.planTargets(context.Background(), "маркетинговые агентства", llm.Interpretation{})
	if len(targets) != 1 || targets[0].Query != "маркетинговые агентства" || targets[0].URL != "" {
		t.Fatalf("без LLM ожидался один поисковый target, получено %+v", targets)
	}
	if tokens != 0 {
		t.Fatalf("без LLM расход токенов %d, ожидался 0", tokens)
	}
}

func TestRunSearchPlanWithLLM(t *testing.T) {
	r := &RunSearch{LLM: &fakeLLM{targets: []llm.Target{{Query: "агентства b2b"}, {URL: "https://acme.example"}}}}
	targets, _ := r.planTargets(context.Background(), "x", llm.Interpretation{})
	if len(targets) != 2 || targets[0].Query != "агентства b2b" || targets[1].URL != "https://acme.example" {
		t.Fatalf("LLM-план не применён: %+v", targets)
	}
}

// TestRunSearchPlanCarriesInterpretation проверяет, что переданная интерпретация
// уходит в llm.PlanRequest: генерация плана опирается на критерии задания.
func TestRunSearchPlanCarriesInterpretation(t *testing.T) {
	llmFake := &fakeLLM{targets: []llm.Target{{Query: "цель"}}}
	r := &RunSearch{LLM: llmFake}
	interpretation := llm.Interpretation{
		TargetLead:  "B2B-агентство",
		NeedSignals: []string{"ищет автоматизацию"},
		Exclusions:  []string{"каталоги", "магазины"},
	}

	r.planTargets(context.Background(), "маркетинговые агентства", interpretation)

	if len(llmFake.planRequests) != 1 {
		t.Fatalf("Plan вызван %d раз, ожидался 1", len(llmFake.planRequests))
	}
	got := llmFake.planRequests[0]
	if got.Interpretation.TargetLead != interpretation.TargetLead ||
		len(got.Interpretation.Exclusions) != 2 || got.Interpretation.Exclusions[0] != "каталоги" {
		t.Fatalf("интерпретация не дошла до Plan: %+v", got.Interpretation)
	}
}

func TestRunSearchPlanWithLLMFallback(t *testing.T) {
	_, metrics, _ := testObservability()
	r := &RunSearch{LLM: &fakeLLM{err: errors.New("llm down")}, Metrics: metrics, ServiceName: "lead-management"}
	targets, _ := r.planTargets(context.Background(), "маркетинговые агентства", llm.Interpretation{})
	if len(targets) != 1 || targets[0].Query != "маркетинговые агентства" {
		t.Fatalf("при ошибке LLM ожидался fallback на запрос: %+v", targets)
	}
	if got := metrics.Counter("downstream_errors_total", map[string]string{"service": "lead-management", "engine": "llm"}); got != 1 {
		t.Fatalf("downstream_errors_total llm = %d, ожидался 1", got)
	}
}

func TestExtractLayer2EmailPhoneCloudflare(t *testing.T) {
	r := &RunSearch{Clock: fakeClock{t: testTime}}
	page := crawl.Page{
		URL:      "https://example.com/contact",
		Markdown: "Email support@example.com or call +1 (234) 567-8901.\n<a data-cfemail=\"026a676e6e6d42677a636f726e672c616d6f\">[email protected]</a>",
	}
	contacts := r.extractLayer2(page)
	if len(contacts) != 3 {
		t.Fatalf("извлечено контактов %d, ожидалось 3", len(contacts))
	}

	byID := make(map[string]contactCandidate)
	for _, c := range contacts {
		byID[c.identity()] = c
	}

	if c, ok := byID["support@example.com"]; !ok || c.email.prov.Method != domain.MethodRegex {
		t.Fatalf("regex-email не извлечён или неверный метод: %+v", byID)
	}
	if c, ok := byID["12345678901"]; !ok || c.phone.prov.Method != domain.MethodRegex {
		t.Fatalf("телефон не извлечён или неверный метод: %+v", byID)
	}
	if c, ok := byID["hello@example.com"]; !ok || c.email.prov.Method != domain.MethodMarkdown {
		t.Fatalf("cloudflare-email не декодирован или неверный метод: %+v", byID)
	}
}

func TestExtractLayer2SkipsDates(t *testing.T) {
	r := &RunSearch{Clock: fakeClock{t: testTime}}
	page := crawl.Page{
		URL:      "https://example.com/contact",
		Markdown: "Опубликовано 2024.01.01. Телефон: +1 (234) 567-8901.",
	}
	contacts := r.extractLayer2(page)
	if len(contacts) != 1 {
		t.Fatalf("извлечено контактов %d, ожидался 1: %+v", len(contacts), contacts)
	}
	if contacts[0].identity() != "12345678901" {
		t.Fatalf("идентификатор %q, ожидался телефон 12345678901", contacts[0].identity())
	}
}

func TestHandleNoEscalationWhenFullCard(t *testing.T) {
	fc := &fakeCrawl{
		pages: map[string][]crawl.Page{
			"https://example.com": {{
				URL:      "https://example.com",
				Markdown: "# Example Corp\n\nLeading agency.\n\ncontact: hello@example.com",
				Metadata: map[string]string{"description": "Leading agency"},
			}},
		},
	}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()

	r := &RunSearch{
		Jobs:     store,
		Profiles: profiles,
		Contacts: contacts,
		Search:   fs,
		Crawl:    fc,
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fc.extractCalls != 0 {
		t.Fatalf("Extract вызван %d раз при полной карточке", fc.extractCalls)
	}
	if len(profiles.byID) != 1 {
		t.Fatalf("карточек %d, ожидалось 1", len(profiles.byID))
	}
}

// TestHandleCrawlFailureDoesNotFailJob проверяет, что сбой обхода одного сайта
// не роняет всё задание: поиск продолжается по следующему сайту, а найденная
// компания сохраняется. Один плохой сайт не должен терять остальные результаты.
func TestHandleCrawlFailureDoesNotFailJob(t *testing.T) {
	fc := &fakeCrawl{
		pages: map[string][]crawl.Page{
			"https://good.example": {{
				URL:      "https://good.example",
				Markdown: "# Good Corp\n\nEngineering.\n\nhello@good.example",
				Metadata: map[string]string{"description": "Engineering"},
			}},
		},
		crawlErrByURL: map[string]error{"https://bad.example": errors.New("firecrawl: unexpected EOF")},
	}
	fs := &fakeSearch{results: []search.Result{
		{URL: "https://bad.example"},
		{URL: "https://good.example"},
	}}
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()

	r := &RunSearch{
		Jobs:     store,
		Profiles: profiles,
		Contacts: contacts,
		Search:   fs,
		Crawl:    fc,
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
		t.Fatalf("Handle не должен падать из-за сбоя обхода одного сайта: %v", err)
	}
	if len(profiles.byID) != 1 {
		t.Fatalf("карточек %d, ожидалась 1 (компания со второго сайта)", len(profiles.byID))
	}
}

// TestHandleCrawlTimeoutDoesNotCancelJob проверяет, что исчерпание дедлайна
// времени на один сайт не отменяет всё задание: частично собранные страницы
// обрабатываются, а обход продолжается со следующего сайта. Дедлайн одного сайта
// — не отмена задания.
func TestHandleCrawlTimeoutDoesNotCancelJob(t *testing.T) {
	fc := &fakeCrawl{
		crawlBlockUntilDone: map[string]bool{"https://slow.example": true},
		pages: map[string][]crawl.Page{
			"https://slow.example": {{
				URL:      "https://slow.example",
				Markdown: "# Slow Corp\n\nhello@slow.example",
				Metadata: map[string]string{"description": "Slow"},
			}},
			"https://ok.example": {{
				URL:      "https://ok.example",
				Markdown: "# Ok Corp\n\nhello@ok.example",
				Metadata: map[string]string{"description": "Ok"},
			}},
		},
	}
	fs := &fakeSearch{results: []search.Result{
		{URL: "https://slow.example"},
		{URL: "https://ok.example"},
	}}
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()
	limits := testCrawlLimits()
	limits.MaxTimeSeconds = 1

	r := &RunSearch{
		Jobs:     store,
		Profiles: profiles,
		Contacts: contacts,
		Search:   fs,
		Crawl:    fc,
		Clock:    fakeClock{t: testTime},
		Limits:   limits,
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
		t.Fatalf("дедлайн одного сайта не должен отменять задание: %v", err)
	}
	if len(profiles.byID) != 2 {
		t.Fatalf("карточек %d, ожидалось 2 (частичные страницы медленного сайта и страница второго сайта)", len(profiles.byID))
	}
}

func TestHandleEscalatesWhenLowQuality(t *testing.T) {
	fc := &fakeCrawl{
		pages: map[string][]crawl.Page{
			"https://example.com": {{
				URL:      "https://example.com",
				Markdown: "# Example Corp\n\nLeading agency.\nhello [at] example [dot] com",
				Metadata: map[string]string{"description": "Leading agency"},
			}},
		},
		extract: map[string][]crawl.ContactCandidate{
			"https://example.com": {{Email: "Hello@Example.com", Role: "CEO"}},
		},
	}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()

	r := &RunSearch{
		Jobs:     store,
		Profiles: profiles,
		Contacts: contacts,
		Search:   fs,
		Crawl:    fc,
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fc.extractCalls != 1 {
		t.Fatalf("Extract должен быть вызван при низком качестве, вызван %d раз", fc.extractCalls)
	}

	companyContacts := contacts.byCompany["example.com"]
	if len(companyContacts) != 1 {
		t.Fatalf("контактов %d, ожидался 1", len(companyContacts))
	}
	for _, c := range companyContacts {
		if c.Email().Value != "hello@example.com" || c.Role().Value != "CEO" {
			t.Fatalf("контакт из extract не сохранён: %+v", c.Snapshot())
		}
		if c.Email().Provenance.Method != domain.MethodLLM {
			t.Fatalf("email из extract должен нести MethodLLM, получено %q", c.Email().Provenance.Method)
		}
	}
}

func TestMergeContactsPriority(t *testing.T) {
	regexProv := domain.AttributeProvenance{SourceURL: "https://example.com", ExtractedAt: testTime, Method: domain.MethodRegex}
	llmProv := domain.AttributeProvenance{SourceURL: "https://example.com", ExtractedAt: testTime, Method: domain.MethodLLM}

	cheap := []contactCandidate{
		{email: contactAttr{value: "a@example.com", prov: regexProv}},
	}
	extracted := []contactCandidate{
		{
			email: contactAttr{value: "a@example.com", prov: llmProv},
			phone: contactAttr{value: "1234567", prov: llmProv},
			role:  contactAttr{value: "CEO", prov: llmProv},
		},
	}

	merged := mergeContacts(cheap, extracted)
	if len(merged) != 1 {
		t.Fatalf("контактов после слияния %d, ожидался 1", len(merged))
	}
	c := merged[0]
	if c.email.value != "a@example.com" {
		t.Fatalf("email перетёрт: %q", c.email.value)
	}
	if c.email.prov.Method != domain.MethodRegex {
		t.Fatalf("происхождение дешёвого email перетёрто: %q", c.email.prov.Method)
	}
	if c.phone.value != "1234567" || c.role.value != "CEO" {
		t.Fatalf("extract не дополнил недостающие поля: %+v", c)
	}
	if c.phone.prov.Method != domain.MethodLLM {
		t.Fatalf("дополненный телефон должен нести MethodLLM: %q", c.phone.prov.Method)
	}
}

func TestMergeContactsAddsNewFromExtract(t *testing.T) {
	llmProv := domain.AttributeProvenance{SourceURL: "https://example.com", ExtractedAt: testTime, Method: domain.MethodLLM}
	cheap := []contactCandidate{{email: contactAttr{value: "a@example.com", prov: llmProv}}}
	extracted := []contactCandidate{{email: contactAttr{value: "b@example.com", prov: llmProv}}}
	merged := mergeContacts(cheap, extracted)
	if len(merged) != 2 {
		t.Fatalf("ожидалось 2 контакта, получено %d", len(merged))
	}
}

func TestHandleDedupEnrichesInsteadOfDuplicating(t *testing.T) {
	fc := &fakeCrawl{
		pages: map[string][]crawl.Page{
			"https://example.com": {
				{
					URL:      "https://example.com/",
					Markdown: "# Example Corp\n\nLeading agency.\n\nhello@example.com",
					Metadata: map[string]string{"description": "Leading agency"},
				},
				{
					URL:      "https://example.com/team",
					Markdown: "# Example Corp\n\nAnother agency.\n\nhello@example.com other@example.com",
					Metadata: map[string]string{"description": "Another agency"},
				},
			},
		},
	}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()

	r := &RunSearch{
		Jobs:     store,
		Profiles: profiles,
		Contacts: contacts,
		Search:   fs,
		Crawl:    fc,
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(profiles.byID) != 1 {
		t.Fatalf("компаний %d, ожидалась 1 (дедупликация)", len(profiles.byID))
	}
	company := profiles.byID["example.com"]
	if company == nil {
		t.Fatal("компания example.com не найдена")
	}
	if company.Description().Value != "Leading agency" {
		t.Fatalf("Enrich перетёр описание: %q", company.Description().Value)
	}

	companyContacts := contacts.byCompany["example.com"]
	if len(companyContacts) != 2 {
		t.Fatalf("контактов %d, ожидалось 2 (без дублей)", len(companyContacts))
	}

	recorded := store.byID[id]
	if recorded == nil {
		t.Fatal("результат задания не записан")
	}
	if len(recorded.ResultRef) != 1 || recorded.ResultRef[0] != "example.com" {
		t.Fatalf("ResultRef = %v, ожидалось [example.com]", recorded.ResultRef)
	}
}

// TestRunSearchPublishesResultDuringRun проверяет, что список найденных компаний
// виден уже во время прогона: к началу обхода второй цели результат задания несёт
// компании первой цели. Это то, что делает экран выполняющегося задания непустым.
func TestRunSearchPublishesResultDuringRun(t *testing.T) {
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://one.example": {{URL: "https://one.example", Markdown: "# One Corp\n\nhello@one.example", Metadata: map[string]string{"description": "One"}}},
		"https://two.example": {{URL: "https://two.example", Markdown: "# Two Corp\n\nhello@two.example", Metadata: map[string]string{"description": "Two"}}},
	}}
	fs := &fakeSearch{byQuery: map[string][]search.Result{
		"one": searchResult("https://one.example"),
		"two": searchResult("https://two.example"),
	}}
	llmPort := &fakeLLM{targets: []llm.Target{{Query: "one"}, {Query: "two"}}}
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

	id := mustEnqueue(t, r, "one two")
	var midRun []string
	fc.onCrawl = func() {
		// К обходу второй цели результат первой уже должен быть записан.
		if len(fc.requests) == 2 {
			midRun = append([]string(nil), store.byID[id].ResultRef...)
		}
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"one two"}`)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(midRun) != 1 || midRun[0] != "one.example" {
		t.Fatalf("во время прогона ResultRef = %v, ожидалось [one.example]", midRun)
	}
	if got := store.byID[id].ResultRef; len(got) != 2 {
		t.Fatalf("итоговый ResultRef = %v, ожидалось 2 компании", got)
	}
}

func TestEnqueueValidation(t *testing.T) {
	store := newMemJobs()
	r := &RunSearch{Jobs: store}
	if _, err := r.Enqueue(context.Background(), "   ", 0); err == nil {
		t.Fatal("пустой запрос должен быть отклонён")
	}
	if _, err := r.Enqueue(context.Background(), "запрос", -1); err == nil {
		t.Fatal("отрицательный бюджет должен быть отклонён")
	}

	job, err := r.Enqueue(context.Background(), "маркетинговые агентства", 15)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job.Query != "маркетинговые агентства" {
		t.Fatalf("Query = %q", job.Query)
	}
	if job.MaxCredits != 15 {
		t.Fatalf("MaxCredits = %d, ожидалось 15", job.MaxCredits)
	}
	if job.Status != jobs.StatusQueued {
		t.Fatalf("Status = %q, ожидался queued", job.Status)
	}
	if store.byID[job.ID] == nil {
		t.Fatal("задание не сохранено в хранилище")
	}
}

func TestEnqueueMany(t *testing.T) {
	store := newMemJobs()
	r := &RunSearch{Jobs: store}

	created, err := r.EnqueueMany(context.Background(), []string{"q1", "q2"}, 0)
	if err != nil {
		t.Fatalf("EnqueueMany: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("заданий %d, ожидалось 2", len(created))
	}
	if created[0].Query != "q1" || created[1].Query != "q2" {
		t.Fatalf("запросы заданий: %+v", created)
	}
	for _, job := range created {
		if store.byID[job.ID] == nil {
			t.Fatalf("задание %s не сохранено", job.ID)
		}
	}

	if _, err := r.EnqueueMany(context.Background(), []string{"q1", "  "}, 0); err == nil {
		t.Fatal("пустой запрос в наборе должен быть отклонён")
	}
}

// searchResult — помощник для объявления результата поиска.
func searchResult(url string) []search.Result {
	return []search.Result{{URL: url}}
}

// fakeBudgetTask — фейковая бюджетная обёртка, делегирующая вызовы вложенным
// портам и считающая обращения.
type fakeBudgetTask struct {
	taskID     string
	maxCredits int
	// available — доступный остаток бюджета в снимке (для проверки добора).
	available int
	search    search.Port
	crawl     crawl.Port

	searchCalls int
	crawlCalls  int
}

func (f *fakeBudgetTask) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	f.searchCalls++
	return f.search.Search(ctx, q)
}

func (f *fakeBudgetTask) Crawl(ctx context.Context, r crawl.Request) (crawl.CrawlOutcome, error) {
	f.crawlCalls++
	return f.crawl.Crawl(ctx, r)
}

func (f *fakeBudgetTask) Extract(ctx context.Context, r crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return f.crawl.Extract(ctx, r)
}

func (f *fakeBudgetTask) Snapshot() budget.Snapshot {
	return budget.Snapshot{MaxCredits: f.maxCredits, Available: f.available}
}

// fakeBudgetFactory — фейковая фабрика бюджета, фиксирующая Begin-вызовы.
type fakeBudgetFactory struct {
	search     search.Port
	crawl      crawl.Port
	maxCredits int
	beginErr   error
	// available — доступный остаток, попадающий в снимок бюджета задачи.
	available int

	begins   []string
	lastTask *fakeBudgetTask
	finished []string
}

func (f *fakeBudgetFactory) Begin(taskID string, maxCredits int) (budget.Task, error) {
	f.begins = append(f.begins, taskID)
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	f.lastTask = &fakeBudgetTask{taskID: taskID, maxCredits: maxCredits, available: f.available, search: f.search, crawl: f.crawl}
	return f.lastTask, nil
}

func (f *fakeBudgetFactory) Finish(_ context.Context, taskID string) error {
	f.finished = append(f.finished, taskID)
	return nil
}

// TestRunSearchBudgetPerJob проверяет, что при заданной фабрике бюджета каждое
// задание создаёт собственный бюджет и обращается к Firecrawl только через него.
func TestRunSearchBudgetPerJob(t *testing.T) {
	fc := &fakeCrawl{
		pages: map[string][]crawl.Page{
			"https://example.com": {{
				URL:      "https://example.com",
				Markdown: "# Example Corp\n\nLeading agency.\n\ncontact: hello@example.com",
				Metadata: map[string]string{"description": "Leading agency"},
			}},
		},
	}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	factory := &fakeBudgetFactory{search: fs, crawl: fc, maxCredits: 20}

	r := &RunSearch{
		Jobs:       newMemJobs(),
		Profiles:   newMemProfiles(),
		Contacts:   newMemContacts(),
		Search:     fs,
		Crawl:      fc,
		Budget:     factory,
		MaxCredits: 5,
		Clock:      fakeClock{t: testTime},
		Limits:     testCrawlLimits(),
		Logger:     observ.NewLoggerWithWriter(io.Discard),
		Metrics:    observ.NewMetrics(),
		Tracer:     observ.NewTracer("lead-management"),
	}

	job, err := r.Enqueue(context.Background(), "example", 20)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	id := job.ID
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: mustPayload(t, "example", 20)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(factory.begins) != 1 || factory.begins[0] != id {
		t.Fatalf("Begin-вызовы = %v, ожидался один с id %s", factory.begins, id)
	}
	if factory.lastTask == nil || factory.lastTask.maxCredits != 5 {
		t.Fatalf("задача не ограничена configured FirecrawlMaxCredits=5: %+v", factory.lastTask)
	}
	if factory.lastTask.searchCalls != 1 {
		t.Fatalf("поиск через бюджет вызван %d раз, ожидался 1", factory.lastTask.searchCalls)
	}
	if factory.lastTask.crawlCalls != 1 {
		t.Fatalf("обход через бюджет вызван %d раз, ожидался 1", factory.lastTask.crawlCalls)
	}
	if len(factory.finished) != 1 || factory.finished[0] != id {
		t.Fatalf("terminal Finish не вызван для job ID: finished=%v want=%s", factory.finished, id)
	}
}

// mustEnqueue ставит задание с бюджетом по умолчанию и возвращает его id.
func mustEnqueue(t *testing.T, r *RunSearch, query string) string {
	t.Helper()
	job, err := r.Enqueue(context.Background(), query, 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return job.ID
}

// TestRunSearchBudgetBeginForEveryJob проверяет новую семантику: при заданной
// фабрике бюджет создаётся для каждого задания, включая задание без явного
// кредитного лимита. Задание с бюджетом получает свой лимит, а задание без него —
// безлимитный бюджет (maxCredits == 0), который измеряет фактический расход.
func TestRunSearchBudgetBeginForEveryJob(t *testing.T) {
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://example.com": {{URL: "https://example.com", Markdown: "# Example\n\nDesc", Metadata: map[string]string{"description": "Desc"}}},
	}}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	factory := &fakeBudgetFactory{search: fs, crawl: fc}

	r := &RunSearch{
		Jobs: newMemJobs(), Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: fs, Crawl: fc, Budget: factory,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	// Задание с явным бюджетом 7 создаёт бюджет с этим лимитом и идёт через него.
	job, err := r.Enqueue(context.Background(), "example", 7)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: job.ID, Payload: mustPayload(t, "example", 7)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(factory.begins) != 1 || factory.begins[0] != job.ID {
		t.Fatalf("Begin-вызовы = %v, ожидался один с id %s", factory.begins, job.ID)
	}
	if factory.lastTask == nil || factory.lastTask.maxCredits != 7 {
		t.Fatalf("задаче передан не бюджет задания: %+v", factory.lastTask)
	}
	if factory.lastTask.searchCalls != 1 || factory.lastTask.crawlCalls != 1 {
		t.Fatalf("поиск/обход не прошли через бюджет: %+v", factory.lastTask)
	}

	// Задание без бюджета всё равно создаёт бюджет с maxCredits == 0: лимит не
	// применяется, но фактический расход измеряется.
	begins := len(factory.begins)
	job2, err := r.Enqueue(context.Background(), "example", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: job2.ID, Payload: mustPayload(t, "example", 0)}); err != nil {
		t.Fatalf("Handle без бюджета: %v", err)
	}
	if len(factory.begins) != begins+1 {
		t.Fatalf("без бюджета Begin должен вызываться для измерения расхода: %v", factory.begins)
	}
	if factory.lastTask == nil || factory.lastTask.maxCredits != 0 {
		t.Fatalf("безлимитный бюджет должен получить maxCredits=0: %+v", factory.lastTask)
	}
	if factory.lastTask.searchCalls != 1 || factory.lastTask.crawlCalls != 1 {
		t.Fatalf("поиск/обход без бюджета не прошли через измеритель: %+v", factory.lastTask)
	}
}

// TestRunSearchAssessesCompanyOnSuccessfulPage проверяет задачу 3.3: при
// успешном разборе страницы и настроенной оценке компания получает оценку
// задания, а распределение вердиктов попадает в прогресс.
func TestRunSearchAssessesCompanyOnSuccessfulPage(t *testing.T) {
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://example.com": {{
			URL:      "https://example.com",
			Markdown: "# Example Corp\n\nLeading agency.\n\ncontact: hello@example.com",
			Metadata: map[string]string{"description": "Leading agency"},
		}},
	}}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	store := newMemJobs()
	assessments := newMemAssessments()
	llmPort := &fakeLLM{
		targets: []llm.Target{{Query: "example"}},
		assessment: llm.Assessment{
			Verdict:    llm.VerdictFits,
			SignalKind: llm.SignalDirect,
			Evidence: []llm.AssessmentEvidence{{
				Criterion:  "нужна услуга",
				Fact:       "заявлена напрямую",
				SourceURL:  "https://example.com",
				Reasoning:  "прямое совпадение с критерием",
				SignalKind: llm.SignalDirect,
			}},
		},
	}

	r := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: fs, Crawl: fc, LLM: llmPort, Assessments: assessments,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	job, err := r.Enqueue(context.Background(), "example", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: job.ID, Payload: mustPayload(t, "example", 0)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assessment, err := assessments.GetByJobAndCompany(context.Background(), job.ID, "example.com")
	if err != nil {
		t.Fatalf("оценка компании не сохранена: %v", err)
	}
	if assessment.Verdict() != domain.VerdictFits {
		t.Fatalf("вердикт = %q, ожидался fits", assessment.Verdict())
	}
	last := store.byID[job.ID].Progress
	if last == nil || last.VerdictFits != 1 || last.VerdictHypothesis != 0 || last.VerdictUnsuitable != 0 {
		t.Fatalf("распределение вердиктов в прогрессе не обновлено: %+v", last)
	}
}

// TestRunSearchRevisesAssessmentWithoutDoubleCount проверяет задачу 7.1:
// повторная оценка компании в том же задании (дозагрузка страниц) заменяет
// прежнюю оценку и не двоит распределение вердиктов.
func TestRunSearchRevisesAssessmentWithoutDoubleCount(t *testing.T) {
	page := crawl.Page{
		URL:      "https://example.com",
		Markdown: "# Example Corp\n\nLeading agency.\nContact: hello@example.com",
		Metadata: map[string]string{"description": "Leading agency"},
	}
	page2 := page
	page2.URL = "https://example.com/about"
	fc := &fakeCrawl{pages: map[string][]crawl.Page{"https://example.com": {page, page2}}}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	store := newMemJobs()
	assessments := newMemAssessments()
	llmPort := &fakeLLM{
		targets: []llm.Target{{Query: "example"}},
		assessQueue: []llm.Assessment{
			{
				Verdict:    llm.VerdictFits,
				SignalKind: llm.SignalDirect,
				Evidence: []llm.AssessmentEvidence{{
					Criterion: "нужна услуга", Fact: "заявлена напрямую",
					SourceURL: "https://example.com", Reasoning: "прямое совпадение", SignalKind: llm.SignalDirect,
				}},
			},
			{
				Verdict:    llm.VerdictHypothesis,
				SignalKind: llm.SignalProfile,
				Evidence: []llm.AssessmentEvidence{{
					Criterion: "профиль", Fact: "отрасль совпадает",
					SourceURL: "https://example.com/about", Reasoning: "только профиль", SignalKind: llm.SignalProfile,
					Missing: "нет заявления о потребности",
				}},
			},
		},
	}

	r := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: fs, Crawl: fc, LLM: llmPort, Assessments: assessments,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	job, err := r.Enqueue(context.Background(), "example", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: job.ID, Payload: mustPayload(t, "example", 0)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	list, err := assessments.ListByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ожидалась одна запись оценки, получено %d", len(list))
	}
	if list[0].Verdict() != domain.VerdictHypothesis {
		t.Fatalf("вердикт после ревизии = %q, ожидался hypothesis", list[0].Verdict())
	}
	last := store.byID[job.ID].Progress
	if last == nil || last.VerdictFits != 0 || last.VerdictHypothesis != 1 || last.VerdictUnsuitable != 0 {
		t.Fatalf("распределение вердиктов двоится: %+v", last)
	}
}

// TestRunSearchSkipsAssessmentOnParseError проверяет задачу 3.3: страница без
// названия и домена помечается как ошибка разбора и НЕ превращается в оценку.
func TestRunSearchSkipsAssessmentOnParseError(t *testing.T) {
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://example.com": {{URL: "", Markdown: ""}},
	}}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	store := newMemJobs()
	assessments := newMemAssessments()
	llmPort := &fakeLLM{targets: []llm.Target{{Query: "example"}}}

	r := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: fs, Crawl: fc, LLM: llmPort, Assessments: assessments,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	job, err := r.Enqueue(context.Background(), "example", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := r.Handle(context.Background(), jobs.Job{ID: job.ID, Payload: mustPayload(t, "example", 0)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	list, err := assessments.ListByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("оценка не должна создаваться при ошибке разбора: %+v", list)
	}
	last := store.byID[job.ID].Progress
	if last == nil || last.ParseErrors != 1 {
		t.Fatalf("ошибка разбора не зафиксирована: %+v", last)
	}
}

// TestRunSearchCancelQueued проверяет отмену queued-задания и идемпотентность.
func TestRunSearchCancelQueued(t *testing.T) {
	store := newMemJobs()
	r := &RunSearch{Jobs: store}

	job, err := r.Enqueue(context.Background(), "example", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := r.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := store.byID[job.ID].Status; got != jobs.StatusCancelled {
		t.Fatalf("статус queued-задания = %q, ожидался cancelled", got)
	}
	// Повторная отмена не меняет исход и не ошибается.
	if err := r.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("повторная Cancel: %v", err)
	}
}

// TestRunSearchCancelRunningSignals проверяет, что отмена выполняющегося задания
// фиксируется в хранилище и доставляется сигналом реестра.
func TestRunSearchCancelRunningSignals(t *testing.T) {
	store := newMemJobs()
	registry := jobs.NewCancelRegistry()
	r := &RunSearch{Jobs: store, Signals: registry}

	job, err := r.Enqueue(context.Background(), "example", 0)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.TakeQueued(context.Background()); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.Register(job.ID, cancel)

	if err := r.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("сигнал отмены не доставлен выполняющемуся заданию")
	}
	if !store.cancelRequests[job.ID] {
		t.Fatal("запрос отмены running-задания не зафиксирован в хранилище")
	}
}

// cancelAfterFirstCrawl — фейк обхода, отменяющий контекст на втором URL.
type cancelAfterFirstCrawl struct {
	cancel context.CancelFunc
	pages  map[string][]crawl.Page
	calls  int
}

func (f *cancelAfterFirstCrawl) Crawl(_ context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	f.calls++
	if f.calls >= 2 {
		f.cancel()
		return crawl.CrawlOutcome{}, context.Canceled
	}
	return crawl.CrawlOutcome{Pages: f.pages[req.StartURL]}, nil
}

func (f *cancelAfterFirstCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// TestRunSearchCancelKeepsSavedCards проверяет, что отмена на середине сохраняет
// карточки по уже обойдённым страницам и не является ошибкой.
func TestRunSearchCancelKeepsSavedCards(t *testing.T) {
	store := newMemJobs()
	profiles := newMemProfiles()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fc := &cancelAfterFirstCrawl{
		cancel: cancel,
		pages: map[string][]crawl.Page{
			"https://a.example.com": {{
				URL:      "https://a.example.com",
				Markdown: "# Alpha Corp\n\nDesc a\nContact: hello@example.com",
				Metadata: map[string]string{"description": "Desc a"},
			}},
		},
	}
	fs := &fakeSearch{results: []search.Result{{URL: "https://a.example.com"}, {URL: "https://b.example.com"}}}

	r := &RunSearch{
		Jobs: store, Profiles: profiles, Contacts: newMemContacts(),
		Search: fs, Crawl: fc,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	err := r.Handle(ctx, jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)})
	if !errors.Is(err, jobs.ErrCancelled) {
		t.Fatalf("Handle = %v, ожидалась ErrCancelled", err)
	}
	if _, err := profiles.FindByID(context.Background(), domain.CompanyID("a.example.com")); err != nil {
		t.Fatalf("карточка по обойдённой странице должна быть сохранена: %v", err)
	}
}

// cancelledCrawlWithPages — фейк обхода, отменяющий родительский контекст
// задания и возвращающий уже добытые страницы вместе с crawl.ErrCancelled
// (как адаптер Firecrawl при отмене оператором).
type cancelledCrawlWithPages struct {
	pages  []crawl.Page
	cancel context.CancelFunc
}

func (f *cancelledCrawlWithPages) Crawl(context.Context, crawl.Request) (crawl.CrawlOutcome, error) {
	if f.cancel != nil {
		f.cancel()
	}
	return crawl.CrawlOutcome{Pages: f.pages}, crawl.ErrCancelled
}

func (f *cancelledCrawlWithPages) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// TestRunSearchCancelDrainsFetchedPages проверяет, что при отмене уже добытые
// страницы проходят экстракцию, карточки по ним сохраняются, а задание
// завершается cancelled — без текста ошибки.
func TestRunSearchCancelDrainsFetchedPages(t *testing.T) {
	store := newMemJobs()
	profiles := newMemProfiles()
	ctx, cancel := context.WithCancel(context.Background())
	crawlPort := &cancelledCrawlWithPages{cancel: cancel, pages: []crawl.Page{
		{URL: "https://a.example.com", Markdown: "# Alpha Corp\n\nDesc a\nContact: hello@example.com", Metadata: map[string]string{"description": "Desc a"}},
		{URL: "https://b.example.com", Markdown: "# Beta LLC\n\nDesc b\nContact: hello@example.com", Metadata: map[string]string{"description": "Desc b"}},
	}}
	fs := &fakeSearch{results: []search.Result{{URL: "https://a.example.com"}}}
	factory := &fakeBudgetFactory{search: fs, crawl: crawlPort}

	r := &RunSearch{
		Jobs: store, Profiles: profiles, Contacts: newMemContacts(),
		Search: fs, Crawl: crawlPort, Budget: factory,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	err := r.Handle(ctx, jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)})
	if !errors.Is(err, jobs.ErrCancelled) {
		t.Fatalf("Handle = %v, ожидалась ErrCancelled", err)
	}
	for _, companyID := range []domain.CompanyID{"a.example.com", "b.example.com"} {
		if _, err := profiles.FindByID(context.Background(), companyID); err != nil {
			t.Fatalf("карточка %q должна быть сохранена при отмене: %v", companyID, err)
		}
	}
	if len(factory.finished) != 1 || factory.finished[0] != id {
		t.Fatalf("Finish не вызван при отменённом задании: %v", factory.finished)
	}
}

// TestRunSearchCancelLogsInfoNotError проверяет, что отмена не логируется как
// падение: в логе нет уровня ошибки и сообщения «задание упало».
func TestRunSearchCancelLogsInfoNotError(t *testing.T) {
	var buf bytes.Buffer
	store := newMemJobs()
	ctx, cancel := context.WithCancel(context.Background())
	crawlPort := &cancelledCrawlWithPages{cancel: cancel, pages: []crawl.Page{
		{URL: "https://a.example.com", Markdown: "# Alpha Corp\n\nDesc a", Metadata: map[string]string{"description": "Desc a"}},
	}}
	r := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: &fakeSearch{results: []search.Result{{URL: "https://a.example.com"}}}, Crawl: crawlPort,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(&buf), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	if err := r.Handle(ctx, jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); !errors.Is(err, jobs.ErrCancelled) {
		t.Fatalf("Handle = %v, ожидалась ErrCancelled", err)
	}
	if logged := buf.String(); strings.Contains(logged, "ERROR") || strings.Contains(logged, "задание упало") {
		t.Fatalf("отмена не должна логироваться как ошибка:\n%s", logged)
	}
}

// TestRunSearchReportsProgress проверяет последовательность фаз и итоговые
// счётчики прогресса.
func TestRunSearchReportsProgress(t *testing.T) {
	store := newMemJobs()
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://example.com": {{URL: "https://example.com", Markdown: "# Example\n\nDesc\nContact: hello@example.com", Metadata: map[string]string{"description": "Desc"}}},
	}}
	fs := &fakeSearch{results: searchResult("https://example.com")}

	r := &RunSearch{
		Jobs: store, Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: fs, Crawl: fc,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "example")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	phases := make([]string, 0)
	for _, p := range store.progressHistory[id] {
		phases = append(phases, p.Phase)
	}
	for _, want := range []string{PhasePlanning, PhaseSearching, PhaseCrawling, PhaseFinalizing} {
		if !containsPhase(phases, want) {
			t.Fatalf("фаза %q не опубликована; история фаз: %v", want, phases)
		}
	}
	last := store.byID[id].Progress
	if last == nil {
		t.Fatal("итоговый прогресс не записан")
	}
	if last.Phase != PhaseFinalizing {
		t.Fatalf("итоговая фаза = %q, ожидалась %q", last.Phase, PhaseFinalizing)
	}
	if last.PagesCrawled != 1 {
		t.Fatalf("обойдено страниц = %d, ожидалось 1", last.PagesCrawled)
	}
	if last.CompaniesFound != 1 {
		t.Fatalf("найдено компаний = %d, ожидалась 1", last.CompaniesFound)
	}
}

// containsPhase сообщает, встречается ли фаза в истории (фазы могут повторяться).
func containsPhase(phases []string, want string) bool {
	for _, p := range phases {
		if p == want {
			return true
		}
	}
	return false
}

// mustPayload кодирует payload задания с бюджетом. Старый payload без бюджета
// проверяется отдельно в тестах портов.
func mustPayload(t *testing.T, query string, maxCredits int) []byte {
	t.Helper()
	raw, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: query, MaxCredits: maxCredits})
	if err != nil {
		t.Fatalf("EncodeSearchJobPayload: %v", err)
	}
	return raw
}

// testObservability собирает набор наблюдаемости для тестов: логгер в пустоту,
// in-memory метрики и noop-трейсер.
func testObservability() (observ.Logger, *observ.MemoryMetrics, observ.Tracer) {
	return observ.NewLoggerWithWriter(io.Discard), observ.NewMetrics().(*observ.MemoryMetrics), observ.NewTracer("lead-management")
}

// adapterCancelledCrawl — фейк обхода, отменяющий родительский контекст и
// отдающий страницы вместе с crawl.ErrCancelled.
type adapterCancelledCrawl struct {
	pages  map[string][]crawl.Page
	cancel context.CancelFunc
}

func (f *adapterCancelledCrawl) Crawl(_ context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	if f.cancel != nil {
		f.cancel()
	}
	return crawl.CrawlOutcome{Pages: f.pages[req.StartURL]}, crawl.ErrCancelled
}

func (f *adapterCancelledCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// TestRunSearchProcessesPagesFromCancelledCrawl проверяет, что признак отмены
// адаптера (страницы вместе с crawl.ErrCancelled) не теряет уже добытые страницы.
func TestRunSearchProcessesPagesFromCancelledCrawl(t *testing.T) {
	profiles := newMemProfiles()
	ctx, cancel := context.WithCancel(context.Background())
	fc := &adapterCancelledCrawl{cancel: cancel, pages: map[string][]crawl.Page{
		"https://a.example.com": {{
			URL:      "https://a.example.com",
			Markdown: "# Alpha Corp\n\nDesc a\nContact: hello@example.com",
			Metadata: map[string]string{"description": "Desc a"},
		}},
	}}
	fs := &fakeSearch{results: searchResult("https://a.example.com")}
	logger, metrics, tracer := testObservability()

	r := &RunSearch{
		Jobs: newMemJobs(), Profiles: profiles, Contacts: newMemContacts(),
		Search: fs, Crawl: fc,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: logger, Metrics: metrics, Tracer: tracer,
	}

	id := mustEnqueue(t, r, "example")
	err := r.Handle(ctx, jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)})
	if !errors.Is(err, jobs.ErrCancelled) {
		t.Fatalf("Handle = %v, ожидалась ErrCancelled", err)
	}
	if _, err := profiles.FindByID(context.Background(), domain.CompanyID("a.example.com")); err != nil {
		t.Fatalf("карточка из отменённого обхода должна быть сохранена: %v", err)
	}
}

// TestHandleRecordsJobMetricsOnSuccess проверяет, что успешное задание
// инкрементирует jobs_total с исходом done и пишет task_id в лог.
func TestHandleRecordsJobMetricsOnSuccess(t *testing.T) {
	fc := &fakeCrawl{
		pages: map[string][]crawl.Page{
			"https://example.com": {{
				URL:      "https://example.com",
				Markdown: "# Example Corp\n\nLeading agency.\n\ncontact: hello@example.com",
				Metadata: map[string]string{"description": "Leading agency"},
			}},
		},
	}
	fs := &fakeSearch{results: searchResult("https://example.com")}
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()

	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	tracer := observ.NewTracer("lead-management")

	r := &RunSearch{
		Jobs:        store,
		Profiles:    profiles,
		Contacts:    contacts,
		Search:      fs,
		Crawl:       fc,
		Clock:       fakeClock{t: testTime},
		Limits:      testCrawlLimits(),
		Logger:      logger,
		Metrics:     metrics,
		Tracer:      tracer,
		ServiceName: "lead-management",
	}

	id := mustEnqueue(t, r, "example")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "done"}); got != 1 {
		t.Fatalf("jobs_total done = %d, ожидался 1", got)
	}
	if _, count := metrics.Observation("jobs_duration_seconds", map[string]string{"service": "lead-management"}); count != 1 {
		t.Fatalf("наблюдений длительности = %d, ожидалось 1", count)
	}
	if !strings.Contains(buf.String(), `"task_id":"`+id+`"`) {
		t.Fatalf("лог не несёт task_id задания: %s", buf.String())
	}
}

// TestHandleSearchFailureIsSkipped проверяет, что отказ поискового запроса не
// роняет задание: запрос пропускается, метрика downstream_errors_total(search)
// растёт, а задание завершается done, а не failed.
func TestHandleSearchFailureIsSkipped(t *testing.T) {
	fs := &fakeSearch{err: errors.New("search down")}
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()

	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	tracer := observ.NewTracer("lead-management")

	r := &RunSearch{
		Jobs:        store,
		Profiles:    profiles,
		Contacts:    contacts,
		Search:      fs,
		Crawl:       &fakeCrawl{},
		Clock:       fakeClock{t: testTime},
		Logger:      logger,
		Metrics:     metrics,
		Tracer:      tracer,
		ServiceName: "lead-management",
	}

	id := mustEnqueue(t, r, "example")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
		t.Fatalf("отказ поиска не должен ронять задание: %v", err)
	}

	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "done"}); got != 1 {
		t.Fatalf("jobs_total done = %d, ожидался 1", got)
	}
	if got := metrics.Counter("downstream_errors_total", map[string]string{"service": "lead-management", "engine": "search"}); got != 1 {
		t.Fatalf("downstream_errors_total search = %d, ожидался 1", got)
	}
	if strings.Contains(buf.String(), "задание упало") {
		t.Fatalf("отказ поиска не должен логироваться как падение: %s", buf.String())
	}
}

// TestHandleRecordsJobMetricsOnFailure проверяет, что упавшее задание
// инкрементирует jobs_total с исходом failed и пишет причину в лог. Фатальность
// проверяется на некорректном payload — единственном отказе до обхода.
func TestHandleRecordsJobMetricsOnFailure(t *testing.T) {
	profiles := newMemProfiles()
	contacts := newMemContacts()
	store := newMemJobs()

	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	tracer := observ.NewTracer("lead-management")

	r := &RunSearch{
		Jobs:        store,
		Profiles:    profiles,
		Contacts:    contacts,
		Search:      &fakeSearch{},
		Crawl:       &fakeCrawl{},
		Clock:       fakeClock{t: testTime},
		Logger:      logger,
		Metrics:     metrics,
		Tracer:      tracer,
		ServiceName: "lead-management",
	}

	id := mustEnqueue(t, r, "example")
	// Пустой запрос в payload — некорректное задание: фатальный отказ до обхода.
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":""}`)}); err == nil {
		t.Fatal("Handle должен вернуть ошибку при некорректном payload")
	}

	if got := metrics.Counter("jobs_total", map[string]string{"service": "lead-management", "outcome": "failed"}); got != 1 {
		t.Fatalf("jobs_total failed = %d, ожидался 1", got)
	}
	if !strings.Contains(buf.String(), "задание упало") {
		t.Fatalf("лог не содержит «задание упало»: %s", buf.String())
	}
}

// TestHandleUsesPlanTargetsAndLimits проверяет, что при непустом плане раннер идёт
// по целям плана и применяет его лимиты, не обращаясь к LLM-планированию.
func TestHandleUsesPlanTargetsAndLimits(t *testing.T) {
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://plan.example/contacts": {{URL: "https://plan.example/contacts", Markdown: "# Contact\n\nhello@plan.example"}},
	}}
	fs := &fakeSearch{results: searchResult("https://search.example")}
	llmFake := &fakeLLM{targets: []llm.Target{{Query: "не должна использоваться"}}}
	r := &RunSearch{
		Jobs:     newMemJobs(),
		Profiles: newMemProfiles(),
		Contacts: newMemContacts(),
		Search:   fs,
		Crawl:    fc,
		LLM:      llmFake,
		Clock:    fakeClock{t: testTime},
		Limits:   CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120, IncludePaths: []string{"contact", "team"}},
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}

	plan := ports.SearchPlan{
		Targets: []ports.PlanTarget{{URL: "https://plan.example/contacts"}},
		Limits:  ports.PlanLimits{MaxDepth: 1, MaxTimeSeconds: 60},
	}
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: "критерии из диалога", Plan: &plan})
	if err != nil {
		t.Fatalf("EncodeSearchJobPayload: %v", err)
	}
	id := mustEnqueue(t, r, "критерии из диалога")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if llmFake.planCalls != 0 {
		t.Fatalf("LLM-планирование вызвано %d раз при непустом плане, ожидалось 0", llmFake.planCalls)
	}
	if len(fs.queries) != 0 {
		t.Fatalf("поиск вызван %d раз для URL-цели, ожидалось 0", len(fs.queries))
	}
	if len(fc.requests) != 1 {
		t.Fatalf("обход вызван %d раз, ожидался 1", len(fc.requests))
	}
	got := fc.requests[0]
	if got.StartURL != "https://plan.example/contacts" {
		t.Fatalf("StartURL: получено %q, ожидался стартовый URL из плана", got.StartURL)
	}
	if got.MaxPages != 25 || got.MaxDepth != 1 || got.MaxTimeSeconds != 60 {
		t.Fatalf("лимиты: получено maxPages=%d maxDepth=%d maxTime=%d, ожидалось 25/1/60", got.MaxPages, got.MaxDepth, got.MaxTimeSeconds)
	}
	if len(got.IncludePaths) != 2 || got.IncludePaths[0] != "contact" {
		t.Fatalf("include_paths: получено %v, ожидалось внутреннее значение конфигурации", got.IncludePaths)
	}
}

// TestHandleFallsBackToPlanningWhenPlanEmpty проверяет, что пустой план откатывает
// раннер на существующее планирование и лимиты конфигурации.
func TestHandleFallsBackToPlanningWhenPlanEmpty(t *testing.T) {
	targets := []llm.Target{{Query: "запрос из LLM"}}
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://search.example": {{URL: "https://search.example", Markdown: "# Example\n\nhello@search.example"}},
	}}
	fs := &fakeSearch{results: searchResult("https://search.example")}
	llmFake := &fakeLLM{targets: targets, planCalls: 0}
	r := &RunSearch{
		Jobs:     newMemJobs(),
		Profiles: newMemProfiles(),
		Contacts: newMemContacts(),
		Search:   fs,
		Crawl:    fc,
		LLM:      llmFake,
		Clock:    fakeClock{t: testTime},
		Limits:   CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer("lead-management"),
	}

	emptyPlan := ports.SearchPlan{}
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: "критерии", Plan: &emptyPlan})
	if err != nil {
		t.Fatalf("EncodeSearchJobPayload: %v", err)
	}
	id := mustEnqueue(t, r, "критерии")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if llmFake.planCalls != 1 {
		t.Fatalf("LLM-планирование вызвано %d раз при пустом плане, ожидался 1", llmFake.planCalls)
	}
	if len(fc.requests) != 1 {
		t.Fatalf("обход вызван %d раз, ожидался 1", len(fc.requests))
	}
	if got := fc.requests[0]; got.MaxPages != 25 || got.MaxDepth != 3 || got.MaxTimeSeconds != 120 {
		t.Fatalf("лимиты без плана: получено maxPages=%d maxDepth=%d maxTime=%d, ожидалось 25/3/120 из конфигурации", got.MaxPages, got.MaxDepth, got.MaxTimeSeconds)
	}
}

// TestEnqueuePlannedCreatesJobWithPlan проверяет, что запуск диалога ставит
// задание с целями из финального Plan и лимитами оператора.
func TestEnqueuePlannedCreatesJobWithPlan(t *testing.T) {
	store := newMemJobs()
	llmFake := &fakeLLM{targets: []llm.Target{{Query: "логистические компании"}, {URL: "https://acme.example"}}}
	r := &RunSearch{Jobs: store, LLM: llmFake, Clock: fakeClock{t: testTime}, Metrics: observ.NewMetrics(), ServiceName: "lead-management"}

	limits := ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 120}
	job, err := r.EnqueuePlanned(context.Background(), "логистика России", nil, limits, 20, 0, 0)
	if err != nil {
		t.Fatalf("EnqueuePlanned: %v", err)
	}
	if job.Query != "логистика России" || job.MaxCredits != 20 {
		t.Fatalf("запись задания: получено %+v", job)
	}
	decoded, err := ports.DecodeSearchJobPayload(store.payload[job.ID])
	if err != nil {
		t.Fatalf("разбор payload: %v", err)
	}
	if decoded.Plan == nil {
		t.Fatal("план не записан в payload задания")
	}
	if len(decoded.Plan.Targets) != 2 {
		t.Fatalf("целей плана: %d, ожидалось 2", len(decoded.Plan.Targets))
	}
	if decoded.Plan.Limits.MaxDepth != 2 || decoded.Plan.Limits.MaxTimeSeconds != 120 {
		t.Fatalf("лимиты плана: получено %+v", decoded.Plan.Limits)
	}
}

// TestEnqueuePlannedRejectsInvalidInput проверяет отказ на пустых критериях и
// некорректных лимитах.
func TestEnqueuePlannedRejectsInvalidInput(t *testing.T) {
	r := &RunSearch{Jobs: newMemJobs(), LLM: &fakeLLM{}, Clock: fakeClock{t: testTime}, Metrics: observ.NewMetrics()}
	if _, err := r.EnqueuePlanned(context.Background(), "   ", nil, ports.PlanLimits{}, 0, 0, 0); err == nil {
		t.Fatal("пустые критерии должны быть отвергнуты")
	}
	if _, err := r.EnqueuePlanned(context.Background(), "логистика", nil, ports.PlanLimits{MaxDepth: -1}, 0, 0, 0); err == nil {
		t.Fatal("отрицательная глубина должна быть отвергнута")
	}
	if _, err := r.EnqueuePlanned(context.Background(), "логистика", nil, ports.PlanLimits{MaxTimeSeconds: -1}, 0, 0, 0); err == nil {
		t.Fatal("отрицательное время должно быть отвергнуто")
	}
	// Невалидная цель плана отвергается до постановки: она не подменяется
	// перевыводом из текста запроса.
	if _, err := r.EnqueuePlanned(context.Background(), "логистика", []ports.PlanTarget{{Query: "a", URL: "https://x.example"}}, ports.PlanLimits{}, 0, 0, 0); err == nil {
		t.Fatal("цель с query и url одновременно должна быть отвергнута")
	}
}

// TestEnqueuePlannedFallsBackOnPlanError проверяет, что при отказе планирования
// план несёт единственную цель-запрос, а не остаётся пустым.
func TestEnqueuePlannedFallsBackOnPlanError(t *testing.T) {
	store := newMemJobs()
	llmFake := &fakeLLM{err: errors.New("llm down")}
	r := &RunSearch{Jobs: store, LLM: llmFake, Clock: fakeClock{t: testTime}, Metrics: observ.NewMetrics(), ServiceName: "lead-management"}

	job, err := r.EnqueuePlanned(context.Background(), "логистика России", nil, ports.PlanLimits{}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueuePlanned: %v", err)
	}
	decoded, err := ports.DecodeSearchJobPayload(store.payload[job.ID])
	if err != nil {
		t.Fatalf("разбор payload: %v", err)
	}
	if decoded.Plan == nil || len(decoded.Plan.Targets) != 1 || decoded.Plan.Targets[0].Query != "логистика России" {
		t.Fatalf("ожидался фолбэк на цель-запрос, получено %+v", decoded.Plan)
	}
}

// TestBuildSearchPlanUsesExplicitTargets проверяет, что при непустых planTargets
// цели берутся дословно, а планирование по запросу к LLM не вызывается.
func TestBuildSearchPlanUsesExplicitTargets(t *testing.T) {
	llmFake := &fakeLLM{targets: []llm.Target{{Query: "не должна использоваться"}}}
	r := &RunSearch{LLM: llmFake, Metrics: observ.NewMetrics()}

	plan, tokens := r.buildSearchPlan(context.Background(), "критерии",
		[]ports.PlanTarget{{Query: "seed-1"}, {URL: "https://acme.example"}}, ports.PlanLimits{MaxDepth: 2}, llm.Interpretation{})

	if len(plan.Targets) != 2 || plan.Targets[0].Query != "seed-1" || plan.Targets[1].URL != "https://acme.example" {
		t.Fatalf("цели плана не взяты дословно: %+v", plan.Targets)
	}
	if plan.Limits.MaxDepth != 2 {
		t.Fatalf("лимиты плана потеряны: %+v", plan.Limits)
	}
	if tokens != 0 {
		t.Fatalf("токены = %d, ожидался 0: планирование по запросу не должно вызываться", tokens)
	}
	if llmFake.planCalls != 0 {
		t.Fatalf("планирование по запросу вызвано %d раз при непустых целях, ожидалось 0", llmFake.planCalls)
	}
}

// TestBuildSearchPlanRejectsInvalidTargetWithoutPlanning проверяет, что невалидная
// цель попадает в план (и отвергается Validate), а не подменяется перевыводом.
func TestBuildSearchPlanRejectsInvalidTargetWithoutPlanning(t *testing.T) {
	llmFake := &fakeLLM{targets: []llm.Target{{Query: "перевывод"}}}
	r := &RunSearch{LLM: llmFake, Metrics: observ.NewMetrics()}

	plan, _ := r.buildSearchPlan(context.Background(), "критерии",
		[]ports.PlanTarget{{Query: "a", URL: "https://x.example"}}, ports.PlanLimits{}, llm.Interpretation{})

	if err := plan.Validate(); err == nil {
		t.Fatal("невалидная цель должна быть отвергнута планом")
	}
	if llmFake.planCalls != 0 {
		t.Fatalf("планирование по запросу вызвано %d раз при невалидной цели, ожидалось 0", llmFake.planCalls)
	}
}

// TestBuildSearchPlanFallsBackToPlanning проверяет фолбэк на планирование по
// запросу при пустых planTargets.
func TestBuildSearchPlanFallsBackToPlanning(t *testing.T) {
	llmFake := &fakeLLM{targets: []llm.Target{{Query: "planned"}}}
	r := &RunSearch{LLM: llmFake, Metrics: observ.NewMetrics()}

	plan, _ := r.buildSearchPlan(context.Background(), "критерии", nil, ports.PlanLimits{}, llm.Interpretation{})

	if len(plan.Targets) != 1 || plan.Targets[0].Query != "planned" {
		t.Fatalf("фолбэк-план не собран: %+v", plan.Targets)
	}
	if llmFake.planCalls != 1 {
		t.Fatalf("планирование по запросу вызвано %d раз, ожидался 1", llmFake.planCalls)
	}
}

// TestHandleCountsAssessmentErrorsAndContinues проверяет, что ошибка оценки не
// прерывает обход и не теряется молча: счётчик AssessmentErrors растёт, а причина
// попадает в лог, при этом компании продолжают сохраняться.
func TestHandleCountsAssessmentErrorsAndContinues(t *testing.T) {
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://a.example": {{URL: "https://a.example", Markdown: "# A Corp\n\nhello@a.example"}},
		"https://b.example": {{URL: "https://b.example", Markdown: "# B Corp\n\nhello@b.example"}},
	}}
	fs := &fakeSearch{results: []search.Result{{URL: "https://a.example"}, {URL: "https://b.example"}}}
	llmFake := &fakeLLM{err: errors.New("оценка недоступна")}
	store := newMemJobs()
	var buf bytes.Buffer
	r := &RunSearch{
		Jobs:        store,
		Profiles:    newMemProfiles(),
		Contacts:    newMemContacts(),
		Search:      fs,
		Crawl:       fc,
		LLM:         llmFake,
		Assessments: newMemAssessments(),
		Clock:       fakeClock{t: testTime},
		Limits:      testCrawlLimits(),
		Logger:      observ.NewLoggerWithWriter(&buf),
		Metrics:     observ.NewMetrics(),
		Tracer:      observ.NewTracer("lead-management"),
	}

	id := mustEnqueue(t, r, "оценка")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: mustPayload(t, "оценка", 0)}); err != nil {
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
	if progress.AssessmentErrors != 2 {
		t.Fatalf("AssessmentErrors = %d, ожидалось 2", progress.AssessmentErrors)
	}
	if progress.CompaniesFound != 2 {
		t.Fatalf("обход не продолжился после ошибок оценки: %+v", progress)
	}
	if log := buf.String(); !strings.Contains(log, "оценка компании не удалась") || !strings.Contains(log, `"diagnostic_code":"assessment_compute_failed"`) || strings.Contains(log, "оценка недоступна") {
		t.Fatalf("лог не содержит безопасную категорию ошибки оценки или содержит raw error: %s", log)
	}
}

// blockingCrawl — фейк обхода, ждущий отмены контекста (дедлайна, поставленного
// app-слоем), чтобы проверить, что дедлайн доходит до порта и завершает задание.
type blockingCrawl struct {
	called int
}

func (f *blockingCrawl) Crawl(ctx context.Context, _ crawl.Request) (crawl.CrawlOutcome, error) {
	f.called++
	<-ctx.Done()
	return crawl.CrawlOutcome{}, crawl.ErrCancelled
}

func (f *blockingCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// TestCrawlSentWithoutDepthOrTime проверяет, что обход без заданных глубины или
// времени всё равно отправляется: лимиты больше не обязательны, а отсутствие
// времени означает «не ограничивать по времени» (дедлайн не ставится).
func TestCrawlSentWithoutDepthOrTime(t *testing.T) {
	cases := []struct {
		name   string
		limits CrawlLimits
	}{
		{name: "без глубины", limits: CrawlLimits{MaxPages: 25, MaxTimeSeconds: 120}},
		{name: "без времени", limits: CrawlLimits{MaxPages: 25, MaxDepth: 3}},
		{name: "без глубины и времени", limits: CrawlLimits{MaxPages: 25}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeCrawl{}
			r := &RunSearch{
				Jobs: newMemJobs(), Profiles: newMemProfiles(), Contacts: newMemContacts(),
				Search: &fakeSearch{results: searchResult("https://example.com")}, Crawl: fc,
				Clock: fakeClock{t: testTime}, Limits: tc.limits,
				Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
			}
			id := mustEnqueue(t, r, "example")
			if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if len(fc.requests) != 1 {
				t.Fatalf("обход должен отправляться без глубины/времени, вызовов %d", len(fc.requests))
			}
		})
	}
}

// TestCrawlDeadlineStopsBlockingCrawl проверяет, что дедлайн времени ставит
// app-слой и доходит до порта: блокирующийся обход прерывается по ctx.Done.
// Истечение дедлайна одного сайта НЕ отменяет задание: цель считается
// обработанной, а задание завершается успешно (дальше цикл идёт по другим целям).
func TestCrawlDeadlineStopsBlockingCrawl(t *testing.T) {
	fc := &blockingCrawl{}
	r := &RunSearch{
		Jobs: newMemJobs(), Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: &fakeSearch{results: searchResult("https://example.com")}, Crawl: fc,
		Clock:  fakeClock{t: testTime},
		Limits: CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 1},
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}
	id := mustEnqueue(t, r, "example")
	err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)})
	if err != nil {
		t.Fatalf("дедлайн одного сайта не должен отменять задание: %v", err)
	}
	if fc.called != 1 {
		t.Fatalf("обход вызван %d раз, ожидался 1", fc.called)
	}
}

// TestCrawlRequestCarriesInternalPageLimit проверяет, что внутренний предохранитель
// страниц из конфигурации уезжает в crawl.Request.MaxPages.
func TestCrawlRequestCarriesInternalPageLimit(t *testing.T) {
	fc := &fakeCrawl{pages: map[string][]crawl.Page{
		"https://example.com": {{URL: "https://example.com", Markdown: "# Example\n\nDesc", Metadata: map[string]string{"description": "Desc"}}},
	}}
	r := &RunSearch{
		Jobs: newMemJobs(), Profiles: newMemProfiles(), Contacts: newMemContacts(),
		Search: &fakeSearch{results: searchResult("https://example.com")}, Crawl: fc,
		Clock:  fakeClock{t: testTime},
		Limits: CrawlLimits{MaxPages: 7, MaxDepth: 2, MaxTimeSeconds: 90},
		Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer("lead-management"),
	}
	id := mustEnqueue(t, r, "example")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(fc.requests) != 1 {
		t.Fatalf("обход вызван %d раз, ожидался 1", len(fc.requests))
	}
	if got := fc.requests[0]; got.MaxPages != 7 || got.MaxDepth != 2 || got.MaxTimeSeconds != 90 {
		t.Fatalf("параметры обхода: получено maxPages=%d maxDepth=%d maxTime=%d, ожидалось 7/2/90", got.MaxPages, got.MaxDepth, got.MaxTimeSeconds)
	}
}

// timeoutCrawlWithPages — фейк обхода, отдающий уже собранные страницы вместе с
// crawl.ErrTimeout (как адаптер Firecrawl при исчерпании времени обхода).
type timeoutCrawlWithPages struct {
	pages map[string][]crawl.Page
}

func (f *timeoutCrawlWithPages) Crawl(_ context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	return crawl.CrawlOutcome{Pages: f.pages[req.StartURL]}, crawl.ErrTimeout
}

func (f *timeoutCrawlWithPages) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// TestRunSearchProcessesPagesFromTimedOutCrawl проверяет, что таймаут обхода одного
// сайта (crawl.ErrTimeout) не отменяет задание: уже добытые страницы сохраняются,
// а задание завершается успешно.
func TestRunSearchProcessesPagesFromTimedOutCrawl(t *testing.T) {
	profiles := newMemProfiles()
	fc := &timeoutCrawlWithPages{pages: map[string][]crawl.Page{
		"https://a.example.com": {{
			URL:      "https://a.example.com",
			Markdown: "# Alpha Corp\n\nDesc a\nContact: hello@example.com",
			Metadata: map[string]string{"description": "Desc a"},
		}},
	}}
	fs := &fakeSearch{results: searchResult("https://a.example.com")}
	logger, metrics, tracer := testObservability()

	r := &RunSearch{
		Jobs: newMemJobs(), Profiles: profiles, Contacts: newMemContacts(),
		Search: fs, Crawl: fc,
		Clock:  fakeClock{t: testTime},
		Limits: testCrawlLimits(),
		Logger: logger, Metrics: metrics, Tracer: tracer,
	}

	id := mustEnqueue(t, r, "example")
	if err := r.Handle(context.Background(), jobs.Job{ID: id, Payload: []byte(`{"query":"example"}`)}); err != nil {
		t.Fatalf("таймаут обхода одного сайта не должен отменять задание: %v", err)
	}
	if _, err := profiles.FindByID(context.Background(), domain.CompanyID("a.example.com")); err != nil {
		t.Fatalf("карточка из частичных страниц должна быть сохранена: %v", err)
	}
}
