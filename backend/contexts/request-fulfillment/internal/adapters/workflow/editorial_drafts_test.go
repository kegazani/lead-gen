package workflowadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/lead-management/emailgeneration"
)

func editorialRequestRepository(t *testing.T) *persistence.Repository {
	t.Helper()
	repo := persistence.New()
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID: "req-editorial-drafts", OfferID: "offer-editorial-drafts", TenantID: "tenant-1",
		Content: "Оффер", Target: "B2B", Actor: "client", At: time.Now().UTC(), EventID: "evt-editorial-drafts",
		ExecutionPurpose: domain.ExecutionPurposeEditorial,
		SenderCompany:    &domain.SenderCompanySnapshot{CompanyName: "ООО Отправитель", Website: "https://sender.example", Description: "CRM", Services: []domain.SenderServiceSnapshot{{Name: "Внедрение", Short: "Кратко", Full: "Полное описание"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), request, 0); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestEditorialDraftsEachUniqueLeadAndPersistsWithoutRecipient(t *testing.T) {
	repo := editorialRequestRepository(t)
	store := persistence.NewDraftStore()
	contract := &stubEmailGeneration{}
	drafter := ContractDrafts{Requests: repo, Contract: contract, Drafts: store}
	result, err := drafter.Draft(context.Background(), DraftInput{RequestID: "req-editorial-drafts", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-a", "lead-b"}, Editorial: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.requests) != 2 || result.Drafted != 2 {
		t.Fatalf("calls/result = %d/%+v", len(contract.requests), result)
	}
	for _, request := range contract.requests {
		if request.SenderCompany == nil || request.SenderCompany.CompanyName != "ООО Отправитель" || request.SenderCompany.Services[0].Full != "Полное описание" {
			t.Fatalf("sender не mapped: %+v", request.SenderCompany)
		}
	}
	stored, err := store.ListByRequest(context.Background(), "req-editorial-drafts", "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[0].RecipientRef != "" || stored[1].RecipientRef != "" {
		t.Fatalf("письма без контакта не сохранены: %+v", stored)
	}
}

func TestEditorialDraftsReturnsErrorForPartialOrZeroGeneration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		leads     []string
		port      *stubEmailGeneration
		wantSaved int
	}{
		{name: "частичный", leads: []string{"good", "bad"}, port: &stubEmailGeneration{errs: map[string]error{"bad": errors.New("LLM unavailable")}}, wantSaved: 1},
		{name: "ноль", leads: []string{"bad"}, port: &stubEmailGeneration{errs: map[string]error{"bad": errors.New("LLM unavailable")}}, wantSaved: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := persistence.NewDraftStore()
			drafter := ContractDrafts{Requests: editorialRequestRepository(t), Contract: tc.port, Drafts: store}
			if _, err := drafter.Draft(context.Background(), DraftInput{RequestID: "req-editorial-drafts", TenantID: "tenant-1", LeadIDs: tc.leads, Editorial: true}); err == nil {
				t.Fatal("неполная редакторская генерация завершилась успехом")
			}
			stored, err := store.ListByRequest(context.Background(), "req-editorial-drafts", "tenant-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) != tc.wantSaved {
				t.Fatalf("сохранено %d, ожидалось %d", len(stored), tc.wantSaved)
			}
		})
	}
}

var _ ports.RequestRepository = (*persistence.Repository)(nil)
var _ emailgeneration.Port = (*stubEmailGeneration)(nil)
