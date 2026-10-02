package crmevent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/contracts/sales-workflow/crmevent"
	"lead-gen/contracts/sales-workflow/envelope"
)

func validEvent() crmevent.CRMEvent {
	event := crmevent.NewCRMEvent(envelope.Envelope{
		EventID:       "evt-1",
		OccurredAt:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
	})
	event.DealID = "deal-1"
	event.CompanyRef = "company-1"
	event.ExternalRef = "crm-1"
	event.ExternalRevision = 2
	event.Stage = crmevent.StageNegotiation
	return event
}

func TestCRMEventValidate(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("корректное событие: %v", err)
	}

	cases := map[string]func(*crmevent.CRMEvent){
		"без deal_id":      func(e *crmevent.CRMEvent) { e.DealID = "" },
		"без external_ref": func(e *crmevent.CRMEvent) { e.ExternalRef = "" },
		"нулевая ревизия":  func(e *crmevent.CRMEvent) { e.ExternalRevision = 0 },
		"неизвестный этап": func(e *crmevent.CRMEvent) { e.Stage = "nonsense" },
		"теряет имя":       func(e *crmevent.CRMEvent) { e.Name = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			event := validEvent()
			mutate(&event)
			if err := event.Validate(); err == nil {
				t.Fatal("некорректное событие не отвергнуто")
			}
		})
	}
}

func TestCRMEventConfirmRequiresProof(t *testing.T) {
	event := validEvent()
	event.ResultConfirmed = true
	if err := event.Validate(); err == nil {
		t.Fatal("подтверждение результата без доказательства не отвергнуто")
	}

	event.ActionRef = "action-1"
	event.EvidenceRef = "evidence-1"
	if err := event.Validate(); err != nil {
		t.Fatalf("подтверждение с доказательством: %v", err)
	}
}

func TestCRMEventInProc(t *testing.T) {
	handler := crmevent.InProc(crmevent.InProcHandler{
		IngestFunc: func(_ context.Context, event crmevent.CRMEvent) (crmevent.IngestResult, error) {
			return crmevent.IngestResult{Applied: true, Stage: event.Stage}, nil
		},
	})
	result, err := handler.Ingest(context.Background(), validEvent())
	if err != nil || !result.Applied {
		t.Fatalf("Ingest: %+v, %v", result, err)
	}

	empty := crmevent.InProc(crmevent.InProcHandler{})
	if _, err := empty.Ingest(context.Background(), validEvent()); !errors.Is(err, crmevent.ErrUnavailable) {
		t.Fatalf("незаданный обработчик: %v", err)
	}
}
