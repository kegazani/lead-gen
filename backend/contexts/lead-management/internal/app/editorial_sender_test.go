package app

import (
	"context"
	"strings"
	"testing"

	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/lead-management/emailgeneration"
)

func TestDraftPromptSeparatesSenderOfferAndRecipientWithoutPII(t *testing.T) {
	profiles := newMemProfiles()
	seedCompanyInTenant(profiles, "tenant-1", "lead.example", "Получатель ООО")
	contacts := newMemContacts()
	seedContact(contacts, "lead.example", "private@lead.example", "Директор")
	llmPort := &fakeLLM{converse: []llm.ConverseReply{{Text: "Тема\n\nТекст"}}}
	useCase := &DraftEmailForLead{Profiles: profiles, Contacts: contacts, LLM: llmPort}
	_, err := useCase.Run(context.Background(), DraftEmailInput{
		TenantID: "tenant-1", LeadID: "lead.example", Criteria: "ОФФЕР ОТПРАВИТЕЛЯ",
		SenderCompany: &emailgeneration.SenderCompany{
			CompanyName: "ОТПРАВИТЕЛЬ ООО", Website: "https://sender.example", Description: "Описание отправителя",
			Services: []emailgeneration.SenderServiceSnapshot{{Name: "Сервис отправителя", Short: "Кратко", Full: "Полное описание"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(llmPort.converseRequests) != 1 {
		t.Fatalf("LLM вызовы = %d", len(llmPort.converseRequests))
	}
	prompt := llmPort.converseRequests[0].History[0].Content
	for _, want := range []string{"ОФФЕР ОТПРАВИТЕЛЯ", "ОТПРАВИТЕЛЬ ООО", "https://sender.example", "Описание отправителя", "Сервис отправителя", "Полное описание", "Получатель ООО", "lead.example"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("промпт не получил %q: %s", want, prompt)
		}
	}
	for _, forbidden := range []string{"private@lead.example", "password", "secret", "phone"} {
		if strings.Contains(strings.ToLower(prompt), strings.ToLower(forbidden)) {
			t.Errorf("промпт содержит запрещённые данные %q", forbidden)
		}
	}
}
