package domain_test

import (
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

func TestResearchApprovalLocksExecutionModeAndSelection(t *testing.T) {
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "request-mode", TenantID: "tenant-mode", OfferID: "offer", Content: "Автоматизация", Target: "Генподрядчики", Actor: "human", At: time.Now(), EventID: "submitted"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = request.StartValidation(domain.TransitionInput{Actor: "operator", At: time.Now(), EventID: "validating"}); err != nil {
		t.Fatal(err)
	}
	selection := domain.ResearchSelection{Quantity: 11, Unit: "company", OfferRevision: 1, Markets: []domain.ResearchMarket{{Country: "RU", Languages: []string{"ru"}}}}
	in := domain.TransitionInput{Actor: "operator", At: time.Now(), EventID: "validated"}
	if _, err = request.ValidateForExecution(in, domain.ExecutionModeResearch, &selection); err != nil {
		t.Fatal(err)
	}
	restored, err := domain.RehydrateRequest(request.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if restored.ExecutionMode() != domain.ExecutionModeResearch || !restored.ResearchSelection().Equal(selection) {
		t.Fatal("режим/границы потеряны после восстановления")
	}
	if _, err = restored.Validate(in); !errors.Is(err, domain.ErrExecutionModeConflict) {
		t.Fatalf("legacy повтор разрешён: %v", err)
	}
	changed := selection.Clone()
	changed.Quantity = 12
	if _, err = restored.ValidateForExecution(in, domain.ExecutionModeResearch, &changed); !errors.Is(err, domain.ErrResearchSelectionConflict) {
		t.Fatalf("цель изменена: %v", err)
	}
	copy := restored.ResearchSelection()
	copy.Markets[0].Languages[0] = "en"
	if restored.ResearchSelection().Markets[0].Languages[0] != "ru" {
		t.Fatal("возвращён изменяемый snapshot")
	}
}
