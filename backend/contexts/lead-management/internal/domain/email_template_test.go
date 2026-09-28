package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var templateTime = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)

func mustCreateTemplate(t *testing.T, subject, body string, variables ...MergeVariable) *EmailTemplate {
	t.Helper()
	template, err := CreateTemplate(CreateTemplateParams{
		ID:        "welcome",
		Name:      "Приветствие",
		Subject:   subject,
		Body:      body,
		Variables: variables,
		CreatedAt: templateTime,
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	return template
}

func TestCreateTemplateRejectsEmptyFields(t *testing.T) {
	cases := []struct {
		name    string
		params  CreateTemplateParams
		wantErr error
	}{
		{"пустое имя", CreateTemplateParams{ID: "t", Name: "  ", Subject: "S", Body: "B"}, ErrEmptyTemplateName},
		{"пустая тема", CreateTemplateParams{ID: "t", Name: "N", Subject: " ", Body: "B"}, ErrEmptyTemplateSubject},
		{"пустое тело", CreateTemplateParams{ID: "t", Name: "N", Subject: "S", Body: "\n"}, ErrEmptyTemplateBody},
		{"пустой идентификатор", CreateTemplateParams{ID: " ", Name: "N", Subject: "S", Body: "B"}, ErrEmptyTemplateID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CreateTemplate(tc.params); !errors.Is(err, tc.wantErr) {
				t.Fatalf("ожидалась %v, получено %v", tc.wantErr, err)
			}
		})
	}
}

func TestTemplateReferenceRoundtrip(t *testing.T) {
	reference, err := NewTemplateReference(TemplateID("welcome"), 3)
	if err != nil {
		t.Fatalf("NewTemplateReference: %v", err)
	}
	if reference.String() != "welcome@3" {
		t.Fatalf("ссылка = %q, ожидалась welcome@3", reference.String())
	}

	parsed, err := ParseTemplateReference("welcome@3")
	if err != nil {
		t.Fatalf("ParseTemplateReference: %v", err)
	}
	if parsed != reference {
		t.Fatalf("разбор дал %+v, ожидалось %+v", parsed, reference)
	}

	for _, raw := range []string{"", "welcome", "@3", "welcome@", "welcome@0", "welcome@x"} {
		if _, err := ParseTemplateReference(raw); !errors.Is(err, ErrInvalidTemplateReference) {
			t.Fatalf("ParseTemplateReference(%q) = %v, ожидалась ErrInvalidTemplateReference", raw, err)
		}
	}
}

func TestMergeVariableValidation(t *testing.T) {
	if _, err := NewMergeVariable("company", "ваша компания"); err != nil {
		t.Fatalf("известный ключ отвергнут: %v", err)
	}
	if _, err := NewMergeVariable("unknown", "x"); !errors.Is(err, ErrUnknownMergeVariable) {
		t.Fatalf("ключ вне каталога: получено %v", err)
	}
	if _, err := NewMergeVariable("company", "   "); !errors.Is(err, ErrEmptyMergeFallback) {
		t.Fatalf("пустой fallback: получено %v", err)
	}
}

func TestPlaceholdersExtractsAllIncludingDuplicates(t *testing.T) {
	got := Placeholders("Привет, {{company}}! {{ contact }} и снова {{company}}.")
	want := []string{"company", "contact", "company"}
	if len(got) != len(want) {
		t.Fatalf("Placeholders = %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Placeholders[%d] = %q, ожидалось %q", i, got[i], want[i])
		}
	}

	// Наивный разбор не поддерживает вложенность: незакрытый плейсхолдер не даёт
	// результата, а вложенные скобки читаются как обычный текст.
	if nested := Placeholders("{{ {{company}} }}"); len(nested) == 0 || nested[0] != "{{company" {
		t.Fatalf("вложенность обработана неожиданно: %v", nested)
	}
	if open := Placeholders("{{company"); len(open) != 0 {
		t.Fatalf("незакрытый плейсхолдер дал %v", open)
	}
	if none := Placeholders("без плейсхолдеров"); len(none) != 0 {
		t.Fatalf("текст без плейсхолдеров дал %v", none)
	}
}

func TestEditTemplateRaisesRevisionKeepsID(t *testing.T) {
	template := mustCreateTemplate(t, "Тема", "Тело")
	if template.Revision() != 1 {
		t.Fatalf("ревизия нового шаблона = %d, ожидалась 1", template.Revision())
	}
	for want := 2; want <= 4; want++ {
		if err := template.EditTemplate(EditTemplateParams{
			Name:      "Приветствие",
			Subject:   "Тема",
			Body:      "Тело",
			UpdatedAt: templateTime.Add(time.Duration(want) * time.Hour),
		}); err != nil {
			t.Fatalf("EditTemplate: %v", err)
		}
		if template.Revision() != want {
			t.Fatalf("ревизия = %d, ожидалась %d", template.Revision(), want)
		}
		if template.ID() != "welcome" {
			t.Fatalf("идентификатор изменился: %q", template.ID())
		}
	}
}

func TestTemplateInvariants(t *testing.T) {
	// I1: шаблон с необъявленным плейсхолдером не создаётся.
	_, err := CreateTemplate(CreateTemplateParams{
		ID: "t", Name: "N", Subject: "S", Body: "Привет, {{company}} и {{unknown}}",
	})
	if !errors.Is(err, ErrUndeclaredPlaceholder) {
		t.Fatalf("I1: ожидалась ErrUndeclaredPlaceholder, получено %v", err)
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("I1: ошибка не называет плейсхолдер: %v", err)
	}

	// I2: переменная вне каталога и переменная без fallback отвергаются.
	if _, err := CreateTemplate(CreateTemplateParams{
		ID: "t", Name: "N", Subject: "S", Body: "B",
		Variables: []MergeVariable{{Key: "nope", Fallback: "x"}},
	}); !errors.Is(err, ErrUnknownMergeVariable) {
		t.Fatalf("I2 (вне каталога): получено %v", err)
	}
	if _, err := CreateTemplate(CreateTemplateParams{
		ID: "t", Name: "N", Subject: "S", Body: "B",
		Variables: []MergeVariable{{Key: MergeVariableCompany, Fallback: ""}},
	}); !errors.Is(err, ErrEmptyMergeFallback) {
		t.Fatalf("I2 (без fallback): получено %v", err)
	}

	// Дубль ключа отвергается.
	if _, err := CreateTemplate(CreateTemplateParams{
		ID: "t", Name: "N", Subject: "S", Body: "B",
		Variables: []MergeVariable{
			{Key: MergeVariableCompany, Fallback: "a"},
			{Key: MergeVariableCompany, Fallback: "b"},
		},
	}); !errors.Is(err, ErrDuplicateMergeVariable) {
		t.Fatalf("дубль переменной: получено %v", err)
	}

	// I3: объявленная, но неиспользуемая переменная допустима.
	template := mustCreateTemplate(t, "Тема", "Тело",
		MergeVariable{Key: MergeVariableCompany, Fallback: "ваша компания"},
	)
	if len(template.Variables()) != 1 {
		t.Fatalf("объявленная переменная потеряна: %+v", template.Variables())
	}
}

func TestTemplateSnapshotRoundtrip(t *testing.T) {
	template := mustCreateTemplate(t, "Тема для {{company}}", "Здравствуйте, {{company}}!",
		MergeVariable{Key: MergeVariableCompany, Fallback: "ваша компания"},
	)
	if err := template.EditTemplate(EditTemplateParams{
		Name:      "Приветствие v2",
		Subject:   "Тема для {{company}}",
		Body:      "Здравствуйте снова, {{company}}!",
		Variables: []MergeVariable{{Key: MergeVariableCompany, Fallback: "компания"}},
		UpdatedAt: templateTime.Add(time.Hour),
	}); err != nil {
		t.Fatalf("EditTemplate: %v", err)
	}

	restored, err := RehydrateEmailTemplate(template.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateEmailTemplate: %v", err)
	}
	if restored.Name() != template.Name() || restored.Revision() != 2 || restored.Reference().String() != "welcome@2" {
		t.Fatalf("восстановленный шаблон = %+v", restored.Snapshot())
	}
	if len(restored.Variables()) != 1 || restored.Variables()[0].Fallback != "компания" {
		t.Fatalf("переменные не восстановлены: %+v", restored.Variables())
	}
}

func TestRehydrateTemplateRejectsInvalidSnapshot(t *testing.T) {
	valid := mustCreateTemplate(t, "Тема", "Тело").Snapshot()

	broken := valid
	broken.Body = "Тело с {{unknown}}"
	if _, err := RehydrateEmailTemplate(broken); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("снимок с необъявленным плейсхолдером: получено %v", err)
	}

	broken = valid
	broken.Revision = 0
	if _, err := RehydrateEmailTemplate(broken); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("снимок с нулевой ревизией: получено %v", err)
	}

	broken = valid
	broken.Name = ""
	if _, err := RehydrateEmailTemplate(broken); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("снимок без имени: получено %v", err)
	}
}

func TestDeleteTemplateGuardsInvariants(t *testing.T) {
	template := mustCreateTemplate(t, "Тема", "Тело")
	if err := template.DeleteTemplate(); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
}
