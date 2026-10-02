package app

import (
	"context"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// mustTemplate создаёт шаблон письма с заданными телом, темой и объявленными
// переменными (паникует при нарушении инварианта — тестовые данные корректны).
func mustTemplate(t *testing.T, subject, body string, variables ...domain.MergeVariable) *domain.EmailTemplate {
	t.Helper()
	template, err := domain.CreateTemplate(domain.CreateTemplateParams{
		ID:        "tpl",
		Name:      "Шаблон",
		Subject:   subject,
		Body:      body,
		Variables: variables,
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	return template
}

func TestComposeEmailSubstitutesCatalogValues(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "ООО Ромашка")
	company, _ := profiles.FindByID(context.Background(), "example.com")
	contacts := newMemContacts()
	seedContact(contacts, "example.com", "anna@example.com", "CEO")
	contact := contacts.byCompany["example.com"]["anna@example.com"]

	template := mustTemplate(t,
		"Тема для {{company}}",
		"Здравствуйте, {{company}}! Роль: {{contact}}.",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
		domain.MergeVariable{Key: domain.MergeVariableContact, Fallback: "коллега"},
	)

	subject, body := ComposeEmail(template, company, contact)
	if subject != "Тема для ООО Ромашка" {
		t.Fatalf("subject = %q", subject)
	}
	if body != "Здравствуйте, ООО Ромашка! Роль: CEO." {
		t.Fatalf("body = %q", body)
	}
}

func TestComposeEmailFallsBackOnEmptyField(t *testing.T) {
	template := mustTemplate(t,
		"{{company}} — {{contact_email}}",
		"Email: {{contact_email}}",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
		domain.MergeVariable{Key: domain.MergeVariableContactEmail, Fallback: "ваш email"},
	)

	subject, body := ComposeEmail(template, nil, nil)
	if subject != "ваша компания — ваш email" {
		t.Fatalf("subject = %q", subject)
	}
	if body != "Email: ваш email" {
		t.Fatalf("body = %q", body)
	}
}

func TestComposeEmailWithOverridesLeavesNoPlaceholders(t *testing.T) {
	template := mustTemplate(t,
		"{{company}}",
		"Привет, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
	)

	subject, body := ComposeEmailWithValues(template, nil, nil, map[string]string{"company": "ООО Ромашка"})
	if subject != "ООО Ромашка" {
		t.Fatalf("subject = %q", subject)
	}
	if body != "Привет, ООО Ромашка!" {
		t.Fatalf("body = %q", body)
	}
	for _, composed := range []string{subject, body} {
		if strings.Contains(composed, "{{") {
			t.Fatalf("в собранном письме остался плейсхолдер: %q", composed)
		}
	}
}

func TestCatalogCoversDomainKeys(t *testing.T) {
	catalog := VariableCatalog()
	seen := make(map[string]bool, len(catalog))
	for _, option := range catalog {
		if !domain.IsMergeVariable(domain.MergeVariableKey(option.Key)) {
			t.Fatalf("ключ каталога %q отсутствует в доменном наборе", option.Key)
		}
		if option.Label == "" || option.Source == "" {
			t.Fatalf("у переменной %q нет подписи или источника", option.Key)
		}
		seen[option.Key] = true
	}
	for _, key := range domain.MergeVariableKeys() {
		if !seen[string(key)] {
			t.Fatalf("ключ домена %q не описан в каталоге приложения", key)
		}
	}
}

func TestCatalogResolversMapToLeadFields(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "ООО Ромашка")
	company, _ := profiles.FindByID(context.Background(), "example.com")
	contacts := newMemContacts()
	seedContact(contacts, "example.com", "anna@example.com", "CEO")
	contact := contacts.byCompany["example.com"]["anna@example.com"]

	want := map[string]string{
		string(domain.MergeVariableCompany):            "ООО Ромашка",
		string(domain.MergeVariableCompanyDomain):      "example.com",
		string(domain.MergeVariableCompanyDescription): "desc",
		string(domain.MergeVariableContact):            "CEO",
		string(domain.MergeVariableContactEmail):       "anna@example.com",
		string(domain.MergeVariableContactPhone):       "",
	}
	for key, expected := range want {
		if got := resolveVariable(key, company, contact); got != expected {
			t.Fatalf("resolveVariable(%q) = %q, ожидалось %q", key, got, expected)
		}
	}
}
