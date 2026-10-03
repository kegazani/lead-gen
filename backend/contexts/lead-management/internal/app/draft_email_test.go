package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lead-gen/contracts/integration-platform/llm"
)

// TestDraftEmailForLeadGeneratesEmail проверяет успешную генерацию: письмо
// приходит от заглушки LLM, а промпт построен из оффера и данных лида.
func TestDraftEmailForLeadGeneratesEmail(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "lead.example", "ООО Ромашка")
	contacts := newMemContacts()
	seedContact(contacts, "lead.example", "sales@lead.example", "Коммерческий директор")

	llmPort := &fakeLLM{converse: []llm.ConverseReply{{
		Text: "Автоматизация продаж для ООО Ромашка\n\nЗдравствуйте! Предлагаем автоматизировать воронку.",
	}}}
	useCase := &DraftEmailForLead{Profiles: profiles, Contacts: contacts, LLM: llmPort}

	draft, err := useCase.Run(context.Background(), DraftEmailInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead.example",
		Criteria: "оффер: автоматизация отдела продаж", Language: "ru", Tone: "деловой",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if draft.Subject != "Автоматизация продаж для ООО Ромашка" {
		t.Fatalf("subject = %q", draft.Subject)
	}
	if !strings.Contains(draft.Body, "Здравствуйте") {
		t.Fatalf("body = %q", draft.Body)
	}
	if draft.Language != "ru" {
		t.Fatalf("language = %q", draft.Language)
	}

	if len(llmPort.converseRequests) != 1 {
		t.Fatalf("вызовов LLM %d, ожидался 1", len(llmPort.converseRequests))
	}
	history := llmPort.converseRequests[0].History
	if len(history) != 1 || history[0].Role != llm.RoleUser {
		t.Fatalf("история = %+v", history)
	}
	prompt := history[0].Content
	for _, want := range []string{"автоматизация отдела продаж", "ООО Ромашка", "lead.example", "Коммерческий директор"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("промпт не содержит %q: %s", want, prompt)
		}
	}
	// Адрес контакта в промпт не попадает: данные лида обезличены.
	if strings.Contains(prompt, "sales@lead.example") {
		t.Fatalf("промпт содержит персональные данные: %s", prompt)
	}
}

// TestDraftEmailForLeadParsesJSONReply проверяет разбор ответа модели в форме
// JSON-объекта {subject, body}.
func TestDraftEmailForLeadParsesJSONReply(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "lead.example", "ООО Ромашка")
	llmPort := &fakeLLM{converse: []llm.ConverseReply{{
		Text: `{"subject":"Тема из JSON","body":"Тело из JSON"}`,
	}}}
	useCase := &DraftEmailForLead{Profiles: profiles, Contacts: newMemContacts(), LLM: llmPort}

	draft, err := useCase.Run(context.Background(), DraftEmailInput{LeadID: "lead.example", Criteria: "оффер"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if draft.Subject != "Тема из JSON" || draft.Body != "Тело из JSON" {
		t.Fatalf("письмо = %+v", draft)
	}
}

// TestDraftEmailForLeadWithoutLLM проверяет честную недоступность без порта LLM:
// письмо не выдумывается.
func TestDraftEmailForLeadWithoutLLM(t *testing.T) {
	useCase := &DraftEmailForLead{Profiles: newMemProfiles(), Contacts: newMemContacts()}
	_, err := useCase.Run(context.Background(), DraftEmailInput{LeadID: "lead.example", Criteria: "оффер"})
	if !errors.Is(err, ErrEmailGenerationUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrEmailGenerationUnavailable", err)
	}
}

// TestDraftEmailForLeadLLMFailureUnavailable проверяет, что сбой вызова LLM не
// выдаётся за письмо.
func TestDraftEmailForLeadLLMFailureUnavailable(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "lead.example", "ООО Ромашка")
	llmPort := &fakeLLM{err: errors.New("сеть недоступна")}
	useCase := &DraftEmailForLead{Profiles: profiles, Contacts: newMemContacts(), LLM: llmPort}

	_, err := useCase.Run(context.Background(), DraftEmailInput{LeadID: "lead.example", Criteria: "оффер"})
	if !errors.Is(err, ErrEmailGenerationUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrEmailGenerationUnavailable", err)
	}
}

// TestDraftEmailForLeadRejectsInvalid проверяет доменные отказы: пустой оффер и
// несуществующий лид.
func TestDraftEmailForLeadRejectsInvalid(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "lead.example", "ООО Ромашка")
	useCase := &DraftEmailForLead{Profiles: profiles, Contacts: newMemContacts(), LLM: &fakeLLM{}}

	if _, err := useCase.Run(context.Background(), DraftEmailInput{LeadID: "lead.example", Criteria: "   "}); !errors.Is(err, ErrInvalidEmailRequest) {
		t.Fatalf("пустой оффер: ошибка = %v, ожидалась ErrInvalidEmailRequest", err)
	}
	if _, err := useCase.Run(context.Background(), DraftEmailInput{Criteria: "оффер"}); !errors.Is(err, ErrInvalidEmailRequest) {
		t.Fatalf("пустой лид: ошибка = %v, ожидалась ErrInvalidEmailRequest", err)
	}
	if _, err := useCase.Run(context.Background(), DraftEmailInput{LeadID: "missing.example", Criteria: "оффер"}); !errors.Is(err, ErrInvalidEmailRequest) {
		t.Fatalf("несуществующий лид: ошибка = %v, ожидалась ErrInvalidEmailRequest", err)
	}
}

// TestDraftEmailForLeadRejectsEmptyBody проверяет, что пустое тело от модели не
// выдаётся за готовое письмо.
func TestDraftEmailForLeadRejectsEmptyBody(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "lead.example", "ООО Ромашка")
	llmPort := &fakeLLM{converse: []llm.ConverseReply{{Text: "   "}}}
	useCase := &DraftEmailForLead{Profiles: profiles, Contacts: newMemContacts(), LLM: llmPort}

	if _, err := useCase.Run(context.Background(), DraftEmailInput{LeadID: "lead.example", Criteria: "оффер"}); !errors.Is(err, ErrEmailGenerationUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrEmailGenerationUnavailable", err)
	}
}
