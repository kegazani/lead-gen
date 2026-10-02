package dealstate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/contracts/sales-workflow/dealstate"
	"lead-gen/contracts/sales-workflow/envelope"
)

func validEvent() dealstate.DealStateEvent {
	event := dealstate.NewDealStateEvent(envelope.Envelope{
		EventID:       "evt-1",
		OccurredAt:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
	})
	event.DealState = dealstate.DealState{
		DealID:           "deal-1",
		CompanyRef:       "company-1",
		Stage:            dealstate.StageProposal,
		ExternalRef:      "crm-1",
		ExternalRevision: 3,
		Version:          4,
	}
	return event
}

func TestDealStateEventValidate(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("корректное событие: %v", err)
	}

	noDeal := validEvent()
	noDeal.DealID = ""
	if err := noDeal.Validate(); err == nil {
		t.Fatal("событие без deal_id не отвергнуто")
	}

	badStage := validEvent()
	badStage.Stage = "nonsense"
	if err := badStage.Validate(); err == nil {
		t.Fatal("неизвестный этап не отвергнут")
	}

	badName := validEvent()
	badName.Name = "other"
	if err := badName.Validate(); err == nil {
		t.Fatal("чужое имя контракта не отвергнуто")
	}
}

func TestDealStateInProcDelegatesAndUnavailable(t *testing.T) {
	port := dealstate.InProc(dealstate.InProcHandler{
		GetFunc: func(_ context.Context, q dealstate.Query) (dealstate.DealState, error) {
			return dealstate.DealState{DealID: q.DealID, Stage: dealstate.StageWon}, nil
		},
	})
	state, err := port.Get(context.Background(), dealstate.Query{DealID: "deal-1", TenantID: "tenant-1"})
	if err != nil || state.DealID != "deal-1" || state.Stage != dealstate.StageWon {
		t.Fatalf("Get: %+v, %v", state, err)
	}

	empty := dealstate.InProc(dealstate.InProcHandler{})
	if _, err := empty.Get(context.Background(), dealstate.Query{}); !errors.Is(err, dealstate.ErrUnavailable) {
		t.Fatalf("незаданный обработчик: %v, ожидалась ErrUnavailable", err)
	}
}
