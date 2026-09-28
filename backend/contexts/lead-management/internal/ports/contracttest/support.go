// Package contracttest содержит общие contract suite портов контекста
// lead-management. Suite запускается против любой реализации через фабрику и
// потому не зависит от адаптера: in-memory и PostgreSQL проверяются одним и тем
// же набором проверок.
//
// Пакет предназначен для использования из тестов адаптеров и импортирует
// только сам ports, его domain и примитив jobs.
package contracttest

import (
	"fmt"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2025, time.January, 2, 10, 0, 0, 0, time.UTC)

// at возвращает момент времени, сдвинутый на hours часов от baseTime.
func at(hours int) time.Time {
	return baseTime.Add(time.Duration(hours) * time.Hour)
}

// mustCompany создаёт карточку компании с детерминированными полями и ненулевым
// происхождением либо завершает тест.
func mustCompany(t *testing.T, domainName string) *domain.Company {
	t.Helper()
	provenance := domain.Provenance{
		SourceURL:   "https://" + domainName + "/",
		ExtractedAt: baseTime,
	}
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain:      domainName,
		Name:        domain.AttributedValue{Value: "Компания " + domainName, Provenance: provenance},
		Description: domain.AttributedValue{Value: "Описание " + domainName, Provenance: provenance},
		Socials: []domain.AttributedValue{
			{Value: "https://x.com/" + domainName, Provenance: provenance},
		},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("создание компании: %v", err)
	}
	return company
}

// assertCompany сравнивает загруженную карточку с ожидаемой по наблюдаемому
// состоянию. Буфер неподтверждённых событий не сравнивается: события — не часть
// сохраняемого состояния агрегата.
func assertCompany(t *testing.T, got, want *domain.Company) {
	t.Helper()
	assertCompanySnapshot(t, got.Snapshot(), want.Snapshot())
}

// assertCompanySnapshot сверяет снимки карточек поэлементно; время сравнивается
// по моменту (Equal), а не по структуре, поэтому сравнение не зависит от
// JSON-сериализации таймстампов в адаптере PostgreSQL.
func assertCompanySnapshot(t *testing.T, got, want domain.CompanySnapshot) {
	t.Helper()
	if got.ID != want.ID {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID, want.ID)
	}
	assertAttributedValue(t, "Name", got.Name, want.Name)
	assertAttributedValue(t, "Description", got.Description, want.Description)
	if len(got.Socials) != len(want.Socials) {
		t.Fatalf("число соцсетей: получено %d, ожидалось %d", len(got.Socials), len(want.Socials))
	}
	for i := range want.Socials {
		assertAttributedValue(t, fmt.Sprintf("Socials[%d]", i), got.Socials[i], want.Socials[i])
	}
	if got.Hidden != want.Hidden {
		t.Fatalf("Hidden: получено %v, ожидалось %v", got.Hidden, want.Hidden)
	}
	if got.Status.Stage != want.Status.Stage {
		t.Fatalf("Status.Stage: получено %q, ожидалось %q", got.Status.Stage, want.Status.Stage)
	}
	if (got.Status.SnoozeUntil == nil) != (want.Status.SnoozeUntil == nil) {
		t.Fatalf("Status.SnoozeUntil: получено %v, ожидалось %v", got.Status.SnoozeUntil, want.Status.SnoozeUntil)
	}
	if got.Status.SnoozeUntil != nil && !got.Status.SnoozeUntil.Equal(*want.Status.SnoozeUntil) {
		t.Fatalf("Status.SnoozeUntil: получено %s, ожидалось %s", got.Status.SnoozeUntil, want.Status.SnoozeUntil)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt: получено %s, ожидалось %s", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("UpdatedAt: получено %s, ожидалось %s", got.UpdatedAt, want.UpdatedAt)
	}
}

func assertAttributedValue(t *testing.T, label string, got, want domain.AttributedValue) {
	t.Helper()
	if got.Value != want.Value {
		t.Fatalf("%s.Value: получено %q, ожидалось %q", label, got.Value, want.Value)
	}
	if got.Provenance.SourceURL != want.Provenance.SourceURL {
		t.Fatalf("%s.Provenance.SourceURL: получено %q, ожидалось %q", label, got.Provenance.SourceURL, want.Provenance.SourceURL)
	}
	if !got.Provenance.ExtractedAt.Equal(want.Provenance.ExtractedAt) {
		t.Fatalf("%s.Provenance.ExtractedAt: получено %s, ожидалось %s", label, got.Provenance.ExtractedAt, want.Provenance.ExtractedAt)
	}
}

// mustContact создаёт контакт с детерминированными полями и ненулевым
// происхождением либо завершает тест.
func mustContact(t *testing.T, id string, companyID domain.CompanyID) *domain.Contact {
	t.Helper()
	provenance := domain.AttributeProvenance{
		SourceURL:   "https://" + companyID.String() + "/",
		ExtractedAt: baseTime,
		Method:      domain.MethodMarkdown,
	}
	email := &domain.ContactAttribute{Value: id + "@example.com", Provenance: provenance}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        id,
		CompanyID: companyID,
		Email:     email,
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("создание контакта: %v", err)
	}
	return contact
}

// mustTemplate создаёт шаблон письма с детерминированными полями либо
// завершает тест. Плейсхолдеры в теме и теле объявлены среди переменных.
func mustTemplate(t *testing.T, id, name string) *domain.EmailTemplate {
	t.Helper()
	template, err := domain.CreateTemplate(domain.CreateTemplateParams{
		ID:      id,
		Name:    name,
		Subject: "Тема для {{company}}",
		Body:    "Здравствуйте, {{company}}! Роль: {{contact}}.",
		Variables: []domain.MergeVariable{
			{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
			{Key: domain.MergeVariableContact, Fallback: "коллега"},
		},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("создание шаблона: %v", err)
	}
	return template
}

// templateWith создаёт шаблон по заданным параметрам либо завершает тест.
func templateWith(t *testing.T, params domain.CreateTemplateParams) *domain.EmailTemplate {
	t.Helper()
	if params.CreatedAt.IsZero() {
		params.CreatedAt = baseTime
	}
	template, err := domain.CreateTemplate(params)
	if err != nil {
		t.Fatalf("создание шаблона: %v", err)
	}
	return template
}

// assertTemplate сравнивает загруженный шаблон с ожидаемым по наблюдаемому
// состоянию.
func assertTemplate(t *testing.T, got, want *domain.EmailTemplate) {
	t.Helper()
	gotSnapshot, wantSnapshot := got.Snapshot(), want.Snapshot()
	if gotSnapshot.ID != wantSnapshot.ID {
		t.Fatalf("ID: получено %q, ожидалось %q", gotSnapshot.ID, wantSnapshot.ID)
	}
	if gotSnapshot.Name != wantSnapshot.Name {
		t.Fatalf("Name: получено %q, ожидалось %q", gotSnapshot.Name, wantSnapshot.Name)
	}
	if gotSnapshot.Subject != wantSnapshot.Subject {
		t.Fatalf("Subject: получено %q, ожидалось %q", gotSnapshot.Subject, wantSnapshot.Subject)
	}
	if gotSnapshot.Body != wantSnapshot.Body {
		t.Fatalf("Body: получено %q, ожидалось %q", gotSnapshot.Body, wantSnapshot.Body)
	}
	if gotSnapshot.Revision != wantSnapshot.Revision {
		t.Fatalf("Revision: получено %d, ожидалось %d", gotSnapshot.Revision, wantSnapshot.Revision)
	}
	if len(gotSnapshot.Variables) != len(wantSnapshot.Variables) {
		t.Fatalf("число переменных: получено %d, ожидалось %d", len(gotSnapshot.Variables), len(wantSnapshot.Variables))
	}
	for i := range wantSnapshot.Variables {
		if gotSnapshot.Variables[i] != wantSnapshot.Variables[i] {
			t.Fatalf("Variables[%d]: получено %+v, ожидалось %+v", i, gotSnapshot.Variables[i], wantSnapshot.Variables[i])
		}
	}
	if !gotSnapshot.CreatedAt.Equal(wantSnapshot.CreatedAt) {
		t.Fatalf("CreatedAt: получено %s, ожидалось %s", gotSnapshot.CreatedAt, wantSnapshot.CreatedAt)
	}
	if !gotSnapshot.UpdatedAt.Equal(wantSnapshot.UpdatedAt) {
		t.Fatalf("UpdatedAt: получено %s, ожидалось %s", gotSnapshot.UpdatedAt, wantSnapshot.UpdatedAt)
	}
}

// mustMessage создаёт исходящее сообщение с детерминированными полями либо
// завершает тест.
func mustMessage(t *testing.T, id string, leadID domain.CompanyID, recipient string) *domain.OutboundMessage {
	t.Helper()
	message, err := domain.CreateOutboundMessage(domain.CreateOutboundMessageParams{
		ID:        id,
		LeadID:    leadID.String(),
		Recipient: recipient,
		Template:  "intro",
		Body:      "тело",
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("создание сообщения: %v", err)
	}
	return message
}

// assertMessage сравнивает загруженное сообщение с ожидаемым по наблюдаемому
// состоянию.
func assertMessage(t *testing.T, got, want *domain.OutboundMessage) {
	t.Helper()
	assertMessageSnapshot(t, got.Snapshot(), want.Snapshot())
}

// assertMessageSnapshot сверяет снимки сообщений поэлементно.
func assertMessageSnapshot(t *testing.T, got, want domain.OutboundMessageSnapshot) {
	t.Helper()
	if got.ID != want.ID {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID, want.ID)
	}
	if got.LeadID != want.LeadID {
		t.Fatalf("LeadID: получено %q, ожидалось %q", got.LeadID, want.LeadID)
	}
	if got.Recipient != want.Recipient {
		t.Fatalf("Recipient: получено %q, ожидалось %q", got.Recipient, want.Recipient)
	}
	if got.Template != want.Template {
		t.Fatalf("Template: получено %q, ожидалось %q", got.Template, want.Template)
	}
	if got.Body != want.Body {
		t.Fatalf("Body: получено %q, ожидалось %q", got.Body, want.Body)
	}
	if got.Status != want.Status {
		t.Fatalf("Status: получено %q, ожидалось %q", got.Status, want.Status)
	}
	if got.Reason != want.Reason {
		t.Fatalf("Reason: получено %q, ожидалось %q", got.Reason, want.Reason)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt: получено %s, ожидалось %s", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("UpdatedAt: получено %s, ожидалось %s", got.UpdatedAt, want.UpdatedAt)
	}
}

// assertContact сравнивает загруженный контакт с ожидаемым по наблюдаемому
// состоянию.
func assertContact(t *testing.T, got, want *domain.Contact) {
	t.Helper()
	assertContactSnapshot(t, got.Snapshot(), want.Snapshot())
}

// assertContactSnapshot сверяет снимки контактов поэлементно.
func assertContactSnapshot(t *testing.T, got, want domain.ContactSnapshot) {
	t.Helper()
	if got.ID != want.ID {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID, want.ID)
	}
	if got.CompanyID != want.CompanyID {
		t.Fatalf("CompanyID: получено %q, ожидалось %q", got.CompanyID, want.CompanyID)
	}
	assertContactAttribute(t, "Email", got.Email, want.Email)
	assertContactAttribute(t, "Phone", got.Phone, want.Phone)
	assertContactAttribute(t, "Role", got.Role, want.Role)
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt: получено %s, ожидалось %s", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("UpdatedAt: получено %s, ожидалось %s", got.UpdatedAt, want.UpdatedAt)
	}
}

func assertContactAttribute(t *testing.T, label string, got, want *domain.ContactAttribute) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("%s: получено %v, ожидалось %v", label, got, want)
	}
	if got == nil {
		return
	}
	if got.Value != want.Value {
		t.Fatalf("%s.Value: получено %q, ожидалось %q", label, got.Value, want.Value)
	}
	if got.Provenance.SourceURL != want.Provenance.SourceURL {
		t.Fatalf("%s.Provenance.SourceURL: получено %q, ожидалось %q", label, got.Provenance.SourceURL, want.Provenance.SourceURL)
	}
	if !got.Provenance.ExtractedAt.Equal(want.Provenance.ExtractedAt) {
		t.Fatalf("%s.Provenance.ExtractedAt: получено %s, ожидалось %s", label, got.Provenance.ExtractedAt, want.Provenance.ExtractedAt)
	}
	if got.Provenance.Method != want.Provenance.Method {
		t.Fatalf("%s.Provenance.Method: получено %q, ожидалось %q", label, got.Provenance.Method, want.Provenance.Method)
	}
}
