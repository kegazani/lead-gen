package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/crawl"
)

func TestPhoneExtractionRequiresEvidence(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"цифры из обращения", "173525620 249375288 249376288 9032007", nil},
		{"неполный локальный номер", "Телефон: 23593473", nil},
		{"разделённые идентификаторы", "173525 620\n249375 288\n249376 288\n903 2007", nil},
		{"URL статьи", "https://dom.ria.com/articles/173525-620.html", nil},
		{"дата и цена", "Дата 12.10.2026\nЦена 1 200 300 400", nil},
		{"код", "```\nPhone: +7 495 123-45-67\n```", nil},
		{"скрипт", "<script>var phone='+74951234567';</script>", nil},
		{"цифры в файле", "image173525-620.png", nil},
		{"идентификатор с плюсом", "Order ID: +17352562000", nil},
		{"телефон с меткой", "Телефон: (044) 123-45-67", []string{"0441234567"}},
		{"tel национальный", "[Позвонить](tel:0441234567)", []string{"0441234567"}},
		{"tel HTML", `<a href="tel:%2B380441234567">Позвонить</a>`, []string{"380441234567"}},
		{"международный", "+380 (44) 123-45-67", []string{"380441234567"}},
		{"дедупликация", "Телефон: +7 495 123-45-67\n[Звонок](tel:+74951234567)", []string{"74951234567"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractPhones(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("телефоны = %v, ожидаются %v", got, tc.want)
			}
		})
	}
}

func TestStructuredContactsMustBeGrounded(t *testing.T) {
	page := crawl.Page{URL: "https://company.example/contact", Markdown: "sales [at] company [dot] example\nPhone: +380 (44) 123-45-67"}
	got := contactCandidatesFromExtract([]crawl.ContactCandidate{
		{Email: "invented@company.example", Phone: "+74959998877"},
		{Email: "sales@company.example", Phone: "+380441234567", Role: "Sales"},
		{Email: "logo@2x.png", Phone: "173525 620"},
	}, page, testTime)
	if len(got) != 1 || got[0].email.value != "sales@company.example" || got[0].phone.value != "380441234567" {
		t.Fatalf("контакты = %+v", got)
	}
}

func TestBothCrawlPathsRejectBadContactPages(t *testing.T) {
	cases := []struct {
		name string
		page crawl.Page
		want bool
	}{
		{"статья из обращения", crawl.Page{URL: "https://dom.ria.com/articles/podryadchik-173525.html", Metadata: map[string]string{"title": "Основные понятия"}, Markdown: "173525 620\n249375 288\n249376 288\n903 2007"}, false},
		{"статья с email издателя", crawl.Page{URL: "https://company.example/blog/article", Metadata: map[string]string{"title": "Статья"}, Markdown: "hello@company.example"}, false},
		{"без контакта", crawl.Page{URL: "https://company.example", Metadata: map[string]string{"title": "Компания"}, Markdown: "Продукт для бизнеса"}, false},
		{"числовой заголовок", crawl.Page{URL: "https://company.example", Metadata: map[string]string{"title": "173525620"}, Markdown: "hello@company.example"}, false},
		{"телефон", crawl.Page{URL: "https://company.example/contact", Metadata: map[string]string{"title": "Компания"}, Markdown: "Телефон: +380441234567"}, true},
		{"email", crawl.Page{URL: "https://company.example/contact", Metadata: map[string]string{"title": "Компания"}, Markdown: "hello@company.example"}, true},
	}
	for _, tc := range cases {
		for _, path := range []string{"request", "job"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				profiles, contacts := newMemProfiles(), newMemContacts()
				provider := &fakeCrawl{extract: map[string][]crawl.ContactCandidate{tc.page.URL: {{Email: "invented@company.example"}}}}
				seen := map[string]struct{}{}
				ctx := context.Background()
				if path == "request" {
					u := &SearchLeadsForRequest{Profiles: profiles, Contacts: contacts, Clock: fakeClock{t: testTime}}
					reason := ""
					_, ok, err := u.savePage(ctx, provider, tc.page, seen, true, &reason)
					if err != nil || ok != tc.want {
						t.Fatalf("сохранение = %v %v", ok, err)
					}
				} else {
					u := &RunSearch{Profiles: profiles, Contacts: contacts, Clock: fakeClock{t: testTime}, Metrics: observ.NewMetrics()}
					rep := newProgressReporter(newMemJobs(), "test", func() time.Time { return testTime }, nil, nil, nil)
					if err := u.processPage(ctx, tc.page, seen, provider, rep, nil); err != nil {
						t.Fatal(err)
					}
				}
				if (len(profiles.byID) > 0) != tc.want || (len(seen) > 0) != tc.want {
					t.Fatalf("сохранённых %d, найденных %d", len(profiles.byID), len(seen))
				}
			})
		}
	}
}

func TestLegacyArticleContactsAreRejectedAndExcludedFromPool(t *testing.T) {
	profiles, contacts := newMemProfiles(), newMemContacts()
	seedSearchLeadsCompany(t, profiles, "dom.ria.com", "Публикация", "строительная компания")
	for _, digits := range []string{"173525620", "249375288", "249376288", "9032007"} {
		c, err := domain.CreateContact(domain.CreateContactParams{ID: digits, CompanyID: "dom.ria.com", Phone: &domain.ContactAttribute{Value: digits, Provenance: domain.AttributeProvenance{SourceURL: "https://dom.ria.com/articles/podryadchik-173525.html", ExtractedAt: testTime, Method: domain.MethodRegex}}, CreatedAt: testTime})
		if err != nil {
			t.Fatal(err)
		}
		if err := contacts.Save(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	s := &Showcase{Profiles: profiles, Contacts: contacts}
	card, err := s.Card(context.Background(), "dom.ria.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(card.Contacts) != 0 || len(card.RejectedContacts) != 4 || card.Phone != "" {
		t.Fatalf("карточка = %+v", card)
	}
	u := &SearchLeadsForRequest{Profiles: profiles, Contacts: contacts}
	result, err := u.Run(context.Background(), SearchLeadsInput{Criteria: "строительная компания", Limit: 1})
	if err != nil || result.Found != 0 {
		t.Fatalf("пул = %+v, %v", result, err)
	}
	if len(contacts.byCompany["dom.ria.com"]) != 4 {
		t.Fatal("старые записи удалены")
	}
}

func TestRecrawlConfirmsExistingPhoneWithoutDuplicateOrLostRole(t *testing.T) {
	profiles, contacts := newMemProfiles(), newMemContacts()
	seedSearchLeadsCompany(t, profiles, "company.example", "Компания", "Описание")
	prov := domain.AttributeProvenance{SourceURL: "https://company.example/old", ExtractedAt: testTime, Method: domain.MethodRegex}
	legacy, err := domain.CreateContact(domain.CreateContactParams{ID: "legacy-contact-id", CompanyID: "company.example", Phone: &domain.ContactAttribute{Value: "74951234567", Provenance: prov}, Role: &domain.ContactAttribute{Value: "Продажи", Provenance: prov}, CreatedAt: testTime})
	if err != nil {
		t.Fatal(err)
	}
	if err := contacts.Save(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	if usableStoredContact(legacy) {
		t.Fatal("старый номер подтверждён без свидетельства")
	}
	at := testTime.Add(time.Hour)
	u := &SearchLeadsForRequest{Profiles: profiles, Contacts: contacts, Clock: fakeClock{t: at}}
	page := crawl.Page{URL: "https://company.example/contact", Metadata: map[string]string{"title": "Компания"}, Markdown: "Телефон: +7 495 123-45-67"}
	_, ok, err := u.savePage(context.Background(), &fakeCrawl{}, page, map[string]struct{}{}, true, nil)
	if err != nil || !ok {
		t.Fatalf("повторный обход: %v %v", ok, err)
	}
	all, err := contacts.ListByCompany(context.Background(), "company.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID() != "legacy-contact-id" || !all[0].CreatedAt().Equal(testTime) || all[0].Role().Value != "Продажи" || !usableStoredContact(all[0]) || all[0].Phone().Provenance.Evidence == "" || !all[0].UpdatedAt().Equal(at) {
		t.Fatalf("подтверждение потеряло поля или создало дубль: %+v", all)
	}
}

func TestLegacyLongNumberWithoutPhoneContextIsRejected(t *testing.T) {
	prov := domain.AttributeProvenance{SourceURL: "https://linear.app/", ExtractedAt: testTime, Method: domain.MethodRegex}
	contact, err := domain.CreateContact(domain.CreateContactParams{ID: "7510913015200", CompanyID: "linear.app", Phone: &domain.ContactAttribute{Value: "7510913015200", Provenance: prov}, CreatedAt: testTime})
	if err != nil {
		t.Fatal(err)
	}
	if storedContactIssue(contact) != "phone_unconfirmed" || usableStoredContact(contact) {
		t.Fatal("длина выдана за доказательство телефона")
	}
}
