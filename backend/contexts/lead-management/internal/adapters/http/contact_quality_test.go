package httpapi_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

func TestCatalogueShowsContactsAndKeepsRejectedLegacyValuesSeparate(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "company.example", "Компания", "Описание")
	seedContact(t, env.contacts, "company.example", "sales@company.example", "+380441234567", "Продажи")
	seedCompany(t, env.profiles, "dom.ria.com", "Публикация", "Описание статьи")
	contact, err := domain.CreateContact(domain.CreateContactParams{ID: "173525620", CompanyID: "dom.ria.com", Phone: &domain.ContactAttribute{Value: "173525620", Provenance: domain.AttributeProvenance{SourceURL: "https://dom.ria.com/articles/173525.html", ExtractedAt: testTime, Method: domain.MethodRegex}}, CreatedAt: testTime})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.contacts.Save(context.Background(), contact); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	for _, path := range []string{"/cards", "/leads"} {
		html := readBody(t, client.get(t, path))
		for _, want := range []string{"sales@company.example", "380441234567", "Контакты найдены", "Нет пригодных контактов"} {
			if !strings.Contains(html, want) {
				t.Fatalf("%s: нет %q", path, want)
			}
		}
		if strings.Contains(html, "173525620") || strings.Contains(html, "data-composition-set") {
			t.Fatalf("%s: числовой мусор или глобальная оценка в каталоге", path)
		}
	}
	html := readBody(t, client.get(t, "/cards/dom.ria.com"))
	if !strings.Contains(html, "Отклонённые старые контакты") || !strings.Contains(html, "173525620") || !strings.Contains(html, "не прошли проверку формата или источника") {
		t.Fatal("исходное значение и причина отклонения недоступны")
	}
	if strings.Contains(html, `class="card-section assessment"`) {
		t.Fatal("в общей карточке осталась оценка соответствия")
	}
}
