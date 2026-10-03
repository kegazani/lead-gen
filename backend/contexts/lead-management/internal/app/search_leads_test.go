package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

// seedSearchLeadsCompany сохраняет карточку компании в in-memory репозиторий.
func seedSearchLeadsCompany(t *testing.T, profiles *memProfiles, id, name, description string) {
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
	if err := profiles.Save(context.Background(), company); err != nil {
		t.Fatalf("Save(%s): %v", id, err)
	}
}

// TestSearchLeadsForRequestFromPool проверяет подбор из существующего пула:
// достаточно лида из пула — краулинг не запускается.
func TestSearchLeadsForRequestFromPool(t *testing.T) {
	profiles := newMemProfiles()
	seedSearchLeadsCompany(t, profiles, "go-shop.example", "Go Shop", "разработка на заказ")
	seedSearchLeadsCompany(t, profiles, "books.example", "Books", "продажа книг")

	searchPort := &fakeSearch{}
	crawlPort := &fakeCrawl{}
	useCase := &SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: newMemContacts(),
		Search:   searchPort,
		Crawl:    crawlPort,
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
	if len(searchPort.queries) != 0 || len(crawlPort.requests) != 0 {
		t.Fatalf("краулинг не должен запускаться: search=%d crawl=%d", len(searchPort.queries), len(crawlPort.requests))
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
		Search:   searchPort,
		Crawl:    crawlPort,
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
		Search:   searchPort,
		Crawl:    crawlPort,
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
		Search:   searchPort,
		Crawl:    crawlPort,
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
