package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

type failingLeadProfileSave struct {
	ports.LeadProfileRepository
	err error
}

func (p failingLeadProfileSave) Save(context.Context, *domain.Company) error { return p.err }

// seedSearchLeadsCompany сохраняет карточку компании в in-memory репозиторий.
func seedSearchLeadsCompany(t *testing.T, profiles *memProfiles, id, name, description string) {
	t.Helper()
	seedSearchLeadsCompanyInTenant(t, profiles, "", id, name, description)
}

// seedSearchLeadsCompanyInTenant сохраняет карточку в заданном тенанте.
func seedSearchLeadsCompanyInTenant(t *testing.T, profiles *memProfiles, tenant, id, name, description string) {
	t.Helper()
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain: id,
		Name: domain.AttributedValue{
			Value:      name,
			Provenance: domain.Provenance{SourceURL: "https://" + id, ExtractedAt: testTime},
		},
		Description: domain.AttributedValue{
			Value:      description,
			Provenance: domain.Provenance{SourceURL: "https://" + id, ExtractedAt: testTime},
		},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany(%s): %v", id, err)
	}
	if err := profiles.Save(ports.WithTenant(context.Background(), tenant), company); err != nil {
		t.Fatalf("Save(%s): %v", id, err)
	}
}

// TestSearchLeadsForRequestScopesPoolByTenant проверяет, что воркер поиска под
// заявку читает пул в тенанте-владельце заявки: карточки другого тенанта в
// подбор не попадают.
func TestSearchLeadsForRequestScopesPoolByTenant(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompanyInTenant(t, profiles, "tenant-a", "a.example", "A Co", "разработка на заказ")
	seedSearchLeadsCompanyInTenant(t, profiles, "tenant-b", "b.example", "B Co", "разработка на заказ")

	useCase := &SearchLeadsForRequest{Profiles: profiles, Contacts: newMemContacts(), Clock: fakeClock{t: testTime}}
	resultA, err := useCase.Run(context.Background(), SearchLeadsInput{TenantID: "tenant-a", Criteria: "разработка", Limit: 5})
	if err != nil {
		t.Fatalf("Run A: %v", err)
	}
	if resultA.Found != 1 || len(resultA.Leads) != 1 || resultA.Leads[0].CompanyID != "a.example" {
		t.Fatalf("пул тенанта A = %+v, ожидалась только карточка a.example", resultA.Leads)
	}

	resultB, err := useCase.Run(context.Background(), SearchLeadsInput{TenantID: "tenant-b", Criteria: "разработка", Limit: 5})
	if err != nil {
		t.Fatalf("Run B: %v", err)
	}
	if resultB.Found != 1 || len(resultB.Leads) != 1 || resultB.Leads[0].CompanyID != "b.example" {
		t.Fatalf("пул тенанта B = %+v, ожидалась только карточка b.example", resultB.Leads)
	}
}

// TestSearchLeadsForRequestFromPool проверяет подбор из существующего пула:
// достаточно лида из пула — краулинг не запускается.
func TestSearchLeadsForRequestFromPool(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "go-shop.example", "Go Shop", "разработка на заказ")
	seedSearchLeadsCompany(t, profiles, "books.example", "Books", "продажа книг")

	useCase := &SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: newMemContacts(),
		Clock:    fakeClock{t: testTime},
	}

	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "разработка", Limit: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Found != 1 || result.FromPool != 1 || result.Crawled != 0 {
		t.Fatalf("результат = %+v, ожидалось Found=1 FromPool=1 Crawled=0", result)
	}
	if len(result.Leads) != 1 || result.Leads[0].CompanyID != "go-shop.example" || result.Leads[0].Origin != LeadOriginPool {
		t.Fatalf("leads = %+v", result.Leads)
	}
}

// TestSearchLeadsForRequestCrawlsDeficit проверяет дозабор краулингом, когда пула
// не хватает: сбойный дефицит закрывается поиском Firecrawl, найденные компании
// сохраняются.
func TestSearchLeadsForRequestCrawlsDeficit(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "pool.example", "Pool Co", "разработка ПО")
	contacts := newMemContacts()

	const crawledURL = "https://crawled.example/about"
	searchPort := &fakeSearch{results: []search.Result{{URL: crawledURL, Title: "Crawled"}}}
	crawlPort := &fakeCrawl{pages: map[string][]crawl.Page{
		crawledURL: {{
			URL:      crawledURL,
			Markdown: "# Crawled Co\n\nразработка на заказ",
			Metadata: map[string]string{"title": "Crawled Co", "description": "разработка"},
		}},
	}}
	useCase := &SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: contacts,
		Budget:   &fakeBudgetFactory{search: searchPort, crawl: crawlPort},
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
	}

	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "разработка", Limit: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Found != 2 || result.FromPool != 1 || result.Crawled != 1 {
		t.Fatalf("результат = %+v, ожидалось Found=2 FromPool=1 Crawled=1", result)
	}
	if len(searchPort.queries) != 1 || searchPort.queries[0].Text != "разработка" {
		t.Fatalf("поиск вызван неверно: %+v", searchPort.queries)
	}
	// Дефицит равен единице: обходится не больше одного результата поиска.
	if got := searchPort.queries[0].MaxResults; got != 1 {
		t.Fatalf("MaxResults = %d, ожидалось 1", got)
	}
	if _, err := profiles.FindByID(context.Background(), domain.CompanyID("crawled.example")); err != nil {
		t.Fatalf("новая компания не сохранена: %v", err)
	}
	var crawledLead *SearchLeadRef
	for i := range result.Leads {
		if result.Leads[i].CompanyID == "crawled.example" {
			crawledLead = &result.Leads[i]
		}
	}
	if crawledLead == nil || crawledLead.Origin != LeadOriginCrawl {
		t.Fatalf("краул-лид = %+v", crawledLead)
	}
	factory := useCase.Budget.(*fakeBudgetFactory)
	if len(factory.finished) != 1 || factory.finished[0] != factory.begins[0] {
		t.Fatalf("Finish не вызван для task scope: begins=%v finished=%v", factory.begins, factory.finished)
	}
}

func TestSearchLeadsForRequestReturnsProfilePersistenceFailure(t *testing.T) {
	const pageURL = "https://persist-failed.example/about"
	saveErr := errors.New("database write failed")
	searchPort := &fakeSearch{results: []search.Result{{URL: pageURL}}}
	crawlPort := &fakeCrawl{pages: map[string][]crawl.Page{pageURL: {{
		URL:      pageURL,
		Markdown: "# Persist Failed Co\n\nОписание компании",
		Metadata: map[string]string{"title": "Persist Failed Co", "description": "Описание компании"},
	}}}}
	profiles := failingLeadProfileSave{LeadProfileRepository: newMemProfiles(), err: saveErr}
	useCase := &SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: newMemContacts(),
		Budget:   &fakeBudgetFactory{search: searchPort, crawl: crawlPort},
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
	}

	_, err := useCase.Run(context.Background(), SearchLeadsInput{RequestID: "req-persist-failed", Criteria: "логистика", Limit: 1})
	if !errors.Is(err, saveErr) {
		t.Fatalf("Run error = %v, ожидается ошибка записи лида", err)
	}
}

// TestSearchLeadsForRequestWithoutCrawl проверяет поиск без подключённого
// краулинга: результат ограничивается пулом и не падает.
func TestSearchLeadsForRequestWithoutCrawl(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "pool.example", "Pool Co", "разработка ПО")
	useCase := &SearchLeadsForRequest{Profiles: profiles, Contacts: newMemContacts(), Clock: fakeClock{t: testTime}}

	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "разработка", Limit: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Found != 1 || result.FromPool != 1 || result.Crawled != 0 {
		t.Fatalf("результат = %+v, ожидалось Found=1 FromPool=1 Crawled=0", result)
	}
}

func TestSearchLeadsForRequestRequiresGuardForDeficit(t *testing.T) {
	useCase := &SearchLeadsForRequest{Profiles: newMemProfiles(), Contacts: newMemContacts()}
	result, err := useCase.Run(context.Background(), SearchLeadsInput{
		RequestID: "guard-required", Criteria: "разработка", Limit: 1,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.PartialReason != searchPartialSearchUnavailable || result.Found != 0 {
		t.Fatalf("без Guard ожидается частичный отказ без платной операции: %+v", result)
	}
}

func TestSearchLeadsForRequestUsesStableTenantScopedBudgetAndConfiguredCap(t *testing.T) {
	var firstTenantScope string
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		factory := &fakeBudgetFactory{search: &fakeSearch{}, crawl: &fakeCrawl{}}
		useCase := &SearchLeadsForRequest{
			Profiles: newMemProfiles(), Contacts: newMemContacts(), Budget: factory,
			MaxCredits: 0,
		}
		input := SearchLeadsInput{RequestID: "same-request", TenantID: tenant, Criteria: "разработка", Limit: 1}
		if _, err := useCase.Run(context.Background(), input); err != nil {
			t.Fatalf("первый Run (%s): %v", tenant, err)
		}
		if _, err := useCase.Run(context.Background(), input); err != nil {
			t.Fatalf("повторный Run (%s): %v", tenant, err)
		}
		if len(factory.begins) != 2 || factory.begins[0] != factory.begins[1] {
			t.Fatalf("scope бюджета не стабилен для RequestID %q: %v", input.RequestID, factory.begins)
		}
		if len(factory.finished) != 2 || factory.finished[0] != factory.begins[0] || factory.finished[1] != factory.begins[1] {
			t.Fatalf("Finish не вызван для обоих terminal путей: begins=%v finished=%v", factory.begins, factory.finished)
		}
		if factory.lastTask.maxCredits != 0 {
			t.Fatalf("в фабрику передан cap=%d, ожидалось настроенное значение 0", factory.lastTask.maxCredits)
		}
		if tenant == "tenant-a" {
			firstTenantScope = factory.begins[0]
		} else if factory.begins[0] == firstTenantScope {
			t.Fatalf("scope разных tenant совпал: %q", factory.begins[0])
		}
	}
}

func TestSearchLeadsBudgetCapacityReturnsPartialWithoutCallingFirecrawl(t *testing.T) {
	searchPort := &fakeSearch{}
	crawlPort := &fakeCrawl{}
	factory := &fakeBudgetFactory{
		search: searchPort, crawl: crawlPort, beginErr: budget.ErrRegistryCapacity,
	}
	useCase := &SearchLeadsForRequest{
		Profiles: newMemProfiles(), Contacts: newMemContacts(), Budget: factory,
	}
	result, err := useCase.Run(context.Background(), SearchLeadsInput{
		RequestID: "capacity-full", Criteria: "разработка", Limit: 1,
	})
	if err != nil {
		t.Fatalf("capacity pressure should be a safe partial result: %v", err)
	}
	if result.PartialReason != searchPartialBudgetCapacity || len(searchPort.queries) != 0 || len(crawlPort.requests) != 0 {
		t.Fatalf("capacity refusal reached paid ports or omitted reason: result=%+v search=%d crawl=%d", result, len(searchPort.queries), len(crawlPort.requests))
	}
}

func TestSearchLeadsFinishesBudgetAfterPartialProviderFailure(t *testing.T) {
	searchPort := &fakeSearch{err: errors.New("controlled provider failure")}
	factory := &fakeBudgetFactory{search: searchPort, crawl: &fakeCrawl{}}
	useCase := &SearchLeadsForRequest{
		Profiles: newMemProfiles(), Contacts: newMemContacts(), Budget: factory,
	}
	input := SearchLeadsInput{RequestID: "partial-failure", TenantID: "tenant-a", Criteria: "разработка", Limit: 1}
	result, err := useCase.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("partial provider failure should remain a partial result: %v", err)
	}
	if result.PartialReason != searchPartialSearchUnavailable || len(factory.finished) != 1 || factory.finished[0] != factory.begins[0] {
		t.Fatalf("budget scope не завершён после partial error: result=%+v begins=%v finished=%v", result, factory.begins, factory.finished)
	}
}

// TestSearchLeadsForRequestRejectsInvalid проверяет доменный отказ на
// несформулированном запросе.
func TestSearchLeadsForRequestRejectsInvalid(t *testing.T) {
	useCase := &SearchLeadsForRequest{Profiles: newMemProfiles(), Contacts: newMemContacts()}
	if _, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "   "}); !errors.Is(err, ErrInvalidSearchCriteria) {
		t.Fatalf("пустой критерий: ошибка = %v, ожидалась ErrInvalidSearchCriteria", err)
	}
	if _, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "ok", Limit: -1}); !errors.Is(err, ErrInvalidSearchCriteria) {
		t.Fatalf("отрицательный лимит: ошибка = %v, ожидалась ErrInvalidSearchCriteria", err)
	}
}

// TestSearchLeadsForRequestIgnoresStopWordsAndCrawls воспроизводит симптом:
// критерий из несвязанных слов не должен матчить карточки, пересекающиеся только
// по служебным словам («для», «и», «на»). Пул фактически пуст — сценарий обязан
// уйти в краулинг.
func TestSearchLeadsForRequestIgnoresStopWordsAndCrawls(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "services.example", "Services", "услуги для бизнеса и дома")
	seedSearchLeadsCompany(t, profiles, "solutions.example", "Solutions", "решения для компаний на рынке")

	searchPort := &fakeSearch{}
	crawlPort := &fakeCrawl{}
	useCase := &SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: newMemContacts(),
		Budget:   &fakeBudgetFactory{search: searchPort, crawl: crawlPort},
		Clock:    fakeClock{t: testTime},
	}

	const criteria = "уникальные квантовые роботы для подводной сварки"
	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: criteria, Limit: 3})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FromPool != 0 {
		t.Fatalf("FromPool = %d, ожидалось 0: служебные слова не дают матчей", result.FromPool)
	}
	if len(searchPort.queries) != 1 || searchPort.queries[0].Text != criteria {
		t.Fatalf("ветка краулинга не запущена: queries = %+v", searchPort.queries)
	}
}

// TestSearchLeadsForRequestRequiresTwoSignificantTokens проверяет порог матча:
// одиночное короткое (3–4 руны) значимое слово не матчит, нужно пересечение по
// двум различным значимым словам.
func TestSearchLeadsForRequestRequiresTwoSignificantTokens(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "one-word.example", "One", "дрон")
	seedSearchLeadsCompany(t, profiles, "two-words.example", "Two", "дрон для леса")
	useCase := &SearchLeadsForRequest{Profiles: profiles, Contacts: newMemContacts()}

	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "дрон лес", Limit: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Found != 1 || result.FromPool != 1 {
		t.Fatalf("результат = %+v, ожидалось Found=1 FromPool=1", result)
	}
	if result.Leads[0].CompanyID != "two-words.example" {
		t.Fatalf("лид = %+v, ожидался two-words.example (пересечение по двум словам)", result.Leads)
	}
}

// TestSearchLeadsForRequestWithoutSignificantTokensCrawls проверяет, что критерий
// из одних служебных слов не матчит ничего и уходит в краулинг.
func TestSearchLeadsForRequestWithoutSignificantTokensCrawls(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "any.example", "Any", "услуги для всех")

	searchPort := &fakeSearch{}
	crawlPort := &fakeCrawl{}
	useCase := &SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: newMemContacts(),
		Budget:   &fakeBudgetFactory{search: searchPort, crawl: crawlPort},
		Clock:    fakeClock{t: testTime},
	}

	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "для и на с the", Limit: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FromPool != 0 {
		t.Fatalf("FromPool = %d, ожидалось 0 (в критерии нет значимых слов)", result.FromPool)
	}
	if len(searchPort.queries) != 1 {
		t.Fatalf("ожидался один поисковый запрос краулинга, queries = %+v", searchPort.queries)
	}
}

// TestSearchLeadsForRequestRanksByMatch проверяет ранжирование пула: карточка с
// большим числом совпавших слов критерия идёт первой, лимит соблюдается.
func TestSearchLeadsForRequestRanksByMatch(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "one.example", "One", "разработка")
	seedSearchLeadsCompany(t, profiles, "two.example", "Two", "разработка мобильных приложений")
	useCase := &SearchLeadsForRequest{Profiles: profiles, Contacts: newMemContacts()}

	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "разработка мобильных приложений", Limit: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Found != 2 {
		t.Fatalf("Found = %d, ожидалось 2", result.Found)
	}
	// two.example совпал по трём словам, one.example — по одному.
	if result.Leads[0].CompanyID != "two.example" {
		t.Fatalf("порядок лидов = %+v, ожидался two.example первым", result.Leads)
	}
}

// timeoutCrawl — фейк обхода, отдающий уже собранные страницы вместе с
// crawl.ErrTimeout (как адаптер Firecrawl при исчерпании времени обхода).
type timeoutCrawl struct {
	pages map[string][]crawl.Page
}

func (f *timeoutCrawl) Crawl(_ context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	return crawl.CrawlOutcome{Pages: f.pages[req.StartURL]}, crawl.ErrTimeout
}

func (f *timeoutCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// TestSearchLeadsForRequestTimeoutKeepsPartialPages проверяет, что таймаут обхода
// (crawl.ErrTimeout) не роняет шаг поиска: уже добытые до таймаута страницы
// сохраняются, и результат несёт краул-лида.
func TestSearchLeadsForRequestTimeoutKeepsPartialPages(t *testing.T) {
	profiles := newMemProfiles()
	const crawledURL = "https://timeout.example/about"
	searchPort := &fakeSearch{results: []search.Result{{URL: crawledURL}}}
	crawlPort := &timeoutCrawl{pages: map[string][]crawl.Page{
		crawledURL: {{
			URL:      crawledURL,
			Markdown: "# Timeout Co\n\nразработка на заказ",
			Metadata: map[string]string{"title": "Timeout Co", "description": "разработка"},
		}},
	}}
	useCase := &SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: newMemContacts(),
		Budget:   &fakeBudgetFactory{search: searchPort, crawl: crawlPort},
		Clock:    fakeClock{t: testTime},
		Limits:   testCrawlLimits(),
	}

	result, err := useCase.Run(context.Background(), SearchLeadsInput{Criteria: "разработка", Limit: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Crawled != 1 || result.Found < 1 {
		t.Fatalf("результат = %+v, ожидался один краул-лид из частичных страниц", result)
	}
	if _, err := profiles.FindByID(context.Background(), domain.CompanyID("timeout.example")); err != nil {
		t.Fatalf("карточка из частичных страниц должна быть сохранена: %v", err)
	}
}
