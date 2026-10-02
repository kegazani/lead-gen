package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// newTemplates собирает прикладной сервис библиотеки шаблонов на in-memory
// адаптерах.
func newTemplates(profiles *memProfiles, contacts *memContacts, templates *memTemplates) *Templates {
	return &Templates{
		Repository: templates,
		Profiles:   profiles,
		Contacts:   contacts,
		Clock:      fakeClock{t: testTime},
	}
}

// companyVariable — объявленная переменная «company» с fallback.
func companyVariable() TemplateVariable {
	return TemplateVariable{Key: string(domain.MergeVariableCompany), Fallback: "ваша компания"}
}

func TestTemplatesCreateEditDelete(t *testing.T) {
	service := newTemplates(newMemProfiles(), newMemContacts(), newMemTemplates())

	created, err := service.CreateTemplate(context.Background(), TemplateInput{
		ID:        "welcome",
		Name:      "Приветствие",
		Subject:   "Тема для {{company}}",
		Body:      "Здравствуйте, {{company}}!",
		Variables: []TemplateVariable{companyVariable()},
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if created.Revision != 1 || created.Reference != "welcome@1" {
		t.Fatalf("созданный шаблон = %+v", created)
	}

	edited, err := service.EditTemplate(context.Background(), TemplateInput{
		ID:        "welcome",
		Name:      "Приветствие v2",
		Subject:   "Тема v2 для {{company}}",
		Body:      "Здравствуйте снова, {{company}}!",
		Variables: []TemplateVariable{companyVariable()},
	})
	if err != nil {
		t.Fatalf("EditTemplate: %v", err)
	}
	if edited.ID != "welcome" {
		t.Fatalf("идентификатор изменился: %q", edited.ID)
	}
	if edited.Revision != 2 || edited.Reference != "welcome@2" {
		t.Fatalf("после правки шаблон = %+v, ожидалась ревизия 2", edited)
	}

	if err := service.DeleteTemplate(context.Background(), "welcome"); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if _, err := service.GetTemplate(context.Background(), "welcome"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("после удаления GetTemplate = %v, ожидалась ErrTemplateNotFound", err)
	}
}

func TestTemplatesRejectsUndeclaredPlaceholder(t *testing.T) {
	service := newTemplates(newMemProfiles(), newMemContacts(), newMemTemplates())

	_, err := service.CreateTemplate(context.Background(), TemplateInput{
		ID:        "bad",
		Name:      "Плохой шаблон",
		Subject:   "Тема",
		Body:      "Привет, {{company}} и {{unknown}}!",
		Variables: []TemplateVariable{companyVariable()},
	})
	if !errors.Is(err, domain.ErrUndeclaredPlaceholder) {
		t.Fatalf("ожидалась ErrUndeclaredPlaceholder, получено %v", err)
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "unknown") {
		t.Fatalf("ошибка не называет необъявленный плейсхолдер: %v", err)
	}
}

func TestTemplatesRejectsUnknownVariableAndEmptyFallback(t *testing.T) {
	service := newTemplates(newMemProfiles(), newMemContacts(), newMemTemplates())

	_, err := service.CreateTemplate(context.Background(), TemplateInput{
		ID: "bad", Name: "N", Subject: "S", Body: "B",
		Variables: []TemplateVariable{{Key: "unknown_key", Fallback: "x"}},
	})
	if !errors.Is(err, domain.ErrUnknownMergeVariable) {
		t.Fatalf("переменная вне каталога: получено %v", err)
	}

	_, err = service.CreateTemplate(context.Background(), TemplateInput{
		ID: "bad", Name: "N", Subject: "S", Body: "B",
		Variables: []TemplateVariable{{Key: string(domain.MergeVariableCompany), Fallback: "  "}},
	})
	if !errors.Is(err, domain.ErrEmptyMergeFallback) {
		t.Fatalf("пустой fallback: получено %v", err)
	}
}

func TestTemplatesListFiltersAndEmptyState(t *testing.T) {
	service := newTemplates(newMemProfiles(), newMemContacts(), newMemTemplates())
	for _, in := range []TemplateInput{
		{ID: "a", Name: "Альфа", Subject: "Тема A", Body: "Тело A"},
		{ID: "b", Name: "Бета", Subject: "Тема B", Body: "Тело B с {{company}}", Variables: []TemplateVariable{companyVariable()}},
	} {
		if _, err := service.CreateTemplate(context.Background(), in); err != nil {
			t.Fatalf("CreateTemplate %s: %v", in.ID, err)
		}
	}

	list, err := service.ListTemplates(context.Background(), "бета", 0)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(list) != 1 || list[0].ID != "b" {
		t.Fatalf("поиск вернул %+v", list)
	}

	empty, err := service.ListTemplates(context.Background(), "нет-такого", 0)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("пустой результат должен быть пустым списком без ошибки: %+v", empty)
	}
}

func TestTemplatesComposeUsesLeadValuesAndFallback(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "ООО Ромашка")
	contacts := newMemContacts()
	seedContact(contacts, "example.com", "anna@example.com", "CEO")
	service := newTemplates(profiles, contacts, newMemTemplates())

	if _, err := service.CreateTemplate(context.Background(), TemplateInput{
		ID:      "intro",
		Name:    "Приветствие",
		Subject: "Тема для {{company}}",
		Body:    "Здравствуйте, {{company}}! Телефон: {{contact_phone}}.",
		Variables: []TemplateVariable{
			companyVariable(),
			{Key: string(domain.MergeVariableContactPhone), Fallback: "нет телефона"},
		},
	}); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	preview, err := service.Compose(context.Background(), "intro", "example.com", nil)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if preview.Subject != "Тема для ООО Ромашка" {
		t.Fatalf("subject = %q", preview.Subject)
	}
	if preview.Body != "Здравствуйте, ООО Ромашка! Телефон: нет телефона." {
		t.Fatalf("body = %q", preview.Body)
	}
	if preview.Reference != "intro@1" {
		t.Fatalf("reference = %q", preview.Reference)
	}

	// Правка значения оператором меняет предпросмотр.
	overridden, err := service.Compose(context.Background(), "intro", "example.com", map[string]string{"company": "Другая компания"})
	if err != nil {
		t.Fatalf("Compose с правкой: %v", err)
	}
	if overridden.Subject != "Тема для Другая компания" {
		t.Fatalf("subject с правкой = %q", overridden.Subject)
	}
}
