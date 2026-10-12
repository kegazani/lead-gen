package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/lead-management/emailgeneration"
	"lead-gen/contracts/lead-management/emailgeneration/httpport"
)

// seedLeadCompany создаёт карточку компании в in-memory репозитории.
func seedLeadCompany(t *testing.T, profiles *memory.LeadProfileRepository, id, name string) {
	t.Helper()
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain: id,
		Name: domain.AttributedValue{
			Value:      name,
			Provenance: domain.Provenance{SourceURL: "https://" + id, ExtractedAt: testTime},
		},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	// Тенант-владелец пула в round-trip тесте — tenant-1: область хранения совпадает
	// с `TenantID` запроса генерации.
	if err := profiles.Save(ports.WithTenant(context.Background(), "tenant-1"), company); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// seedLeadContact сохраняет контакт компании с email в in-memory репозитории.
func seedLeadContact(t *testing.T, contacts *memory.ContactRepository, companyID, id, email string) {
	t.Helper()
	prov := domain.AttributeProvenance{SourceURL: "https://" + companyID, ExtractedAt: testTime, Method: domain.MethodRegex}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        id,
		CompanyID: domain.CompanyID(companyID),
		Email:     &domain.ContactAttribute{Value: email, Provenance: prov},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if err := contacts.Save(context.Background(), contact); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// newEmailGenerationClient собирает сервер роли api с генерацией письма и сетевой
// клиент контракта email-generation к нему (round-trip).
func newEmailGenerationClient(t *testing.T, useCase *app.DraftEmailForLead) *httpport.Client {
	t.Helper()
	server := httpapi.NewServer(httpapi.Dependencies{EmailGeneration: useCase})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client
}

// TestEmailGenerationHTTPRoundTrip проверяет сквозной путь контракта: сетевой
// клиент → POST /email-generation → use case → письмо, читаемое клиентом.
func TestEmailGenerationHTTPRoundTrip(t *testing.T) {
	profiles := memory.NewLeadProfileRepository()
	seedLeadCompany(t, profiles, "lead.example", "ООО Ромашка")
	llmPort := &fakeLLM{replies: []llm.ConverseReply{{
		Text: "Тема письма\n\nЗдравствуйте! Предлагаем автоматизацию.",
	}}}
	useCase := &app.DraftEmailForLead{
		Profiles: profiles,
		Contacts: memory.NewContactRepository(),
		LLM:      llmPort,
	}
	client := newEmailGenerationClient(t, useCase)

	email, err := client.Generate(context.Background(), emailgeneration.Request{
		RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead.example",
		Criteria: "оффер: автоматизация продаж", Language: "ru",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if email.Subject != "Тема письма" || email.Body != "Здравствуйте! Предлагаем автоматизацию." {
		t.Fatalf("письмо = %+v", email)
	}
	// Контакта с адресом нет: ссылка на получателя честно пуста.
	if email.RecipientRef != "" {
		t.Fatalf("RecipientRef = %q, ожидалась пустая ссылка", email.RecipientRef)
	}
}

// TestEmailGenerationHTTPRecipientRef пробрасывает непрозрачную ссылку на
// получателя через wire: контакт с пригодным адресом даёт непустой `recipient_ref`,
// равный идентификатору контакта, а не адресу.
func TestEmailGenerationHTTPRecipientRef(t *testing.T) {
	profiles := memory.NewLeadProfileRepository()
	seedLeadCompany(t, profiles, "lead.example", "ООО Ромашка")
	contacts := memory.NewContactRepository()
	seedLeadContact(t, contacts, "lead.example", "contact-1", "sales@lead.example")
	useCase := &app.DraftEmailForLead{
		Profiles: profiles,
		Contacts: contacts,
		LLM:      &fakeLLM{replies: []llm.ConverseReply{{Text: "Тема письма\n\nЗдравствуйте! Предлагаем автоматизацию."}}},
	}
	client := newEmailGenerationClient(t, useCase)

	email, err := client.Generate(context.Background(), emailgeneration.Request{
		RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead.example",
		Criteria: "оффер: автоматизация продаж",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if email.RecipientRef != "contact-1" {
		t.Fatalf("RecipientRef = %q, ожидался id контакта", email.RecipientRef)
	}
	if email.RecipientRef == "sales@lead.example" {
		t.Fatalf("RecipientRef не должен быть адресом: %q", email.RecipientRef)
	}
}

// TestEmailGenerationHTTPRejectsInvalid проверяет перевод доменного отказа в 400 и
// ErrRejected на стороне клиента.
func TestEmailGenerationHTTPRejectsInvalid(t *testing.T) {
	useCase := &app.DraftEmailForLead{
		Profiles: memory.NewLeadProfileRepository(),
		Contacts: memory.NewContactRepository(),
		LLM:      &fakeLLM{},
	}
	client := newEmailGenerationClient(t, useCase)

	if _, err := client.Generate(context.Background(), emailgeneration.Request{LeadID: "missing.example", Criteria: "оффер"}); !errors.Is(err, emailgeneration.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestEmailGenerationHTTPUnavailable проверяет, что незамонтированный контракт
// честно отвечает недоступностью, а не пустым письмом; то же — без порта LLM.
func TestEmailGenerationHTTPUnavailable(t *testing.T) {
	client := newEmailGenerationClient(t, nil)
	if _, err := client.Generate(context.Background(), emailgeneration.Request{LeadID: "lead.example", Criteria: "оффер"}); !errors.Is(err, emailgeneration.ErrUnavailable) {
		t.Fatalf("незамонтированный контракт: ошибка = %v, ожидалась ErrUnavailable", err)
	}

	profiles := memory.NewLeadProfileRepository()
	seedLeadCompany(t, profiles, "lead.example", "ООО Ромашка")
	withoutLLM := &app.DraftEmailForLead{Profiles: profiles, Contacts: memory.NewContactRepository()}
	client = newEmailGenerationClient(t, withoutLLM)
	if _, err := client.Generate(context.Background(), emailgeneration.Request{LeadID: "lead.example", Criteria: "оффер"}); !errors.Is(err, emailgeneration.ErrUnavailable) {
		t.Fatalf("без LLM: ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
