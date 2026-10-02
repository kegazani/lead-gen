package inmemory

import (
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
)

var testInstant = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func header(eventID string) domain.EventHeader {
	return domain.EventHeader{
		EventID:       domain.EventID(eventID),
		OccurredAt:    testInstant,
		TenantID:      domain.TenantID("tenant-1"),
		CorrelationID: domain.CorrelationID("corr-1"),
	}
}

func TestEncodeMessageRoutesByContract(t *testing.T) {
	cases := []struct {
		name     string
		event    domain.DomainEvent
		contract string
	}{
		{
			name:     "deal-state-changed",
			event:    domain.DealStateChanged{EventHeader: header("evt-1"), DealID: "deal-1", To: domain.StageLead, Actor: "op"},
			contract: "deal-state.v1",
		},
		{
			name: "result-attributed",
			event: domain.ResultAttributed{
				EventHeader: header("evt-2"), DealID: "deal-1",
				ActionRef: "action-1", EvidenceRef: "evidence-1", ExternalRef: "crm-1", ConfirmedAt: testInstant,
			},
			contract: "attributed-result.v1",
		},
		{
			name:     "crm-sync-conflict",
			event:    domain.CRMSyncConflict{EventHeader: header("evt-3"), SyncID: "sync-1", DealID: "deal-1", Reason: "пропуск"},
			contract: "crm-sync-conflict.v1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message, err := EncodeMessage(tc.event)
			if err != nil {
				t.Fatalf("EncodeMessage: %v", err)
			}
			if message.Exchange != "sales-workflow" {
				t.Fatalf("обменник %q", message.Exchange)
			}
			if message.RoutingKey != tc.contract {
				t.Fatalf("ключ маршрутизации %q, ожидался %q", message.RoutingKey, tc.contract)
			}
			if message.ID != string(tc.event.Header().EventID) {
				t.Fatalf("ID записи %q", message.ID)
			}
			if message.Headers["event_name"] != string(tc.event.Name()) {
				t.Fatalf("заголовок event_name: %q", message.Headers["event_name"])
			}
		})
	}
}

func TestEncodeMessageRejectsEventWithoutID(t *testing.T) {
	_, err := EncodeMessage(domain.DealStateChanged{EventHeader: header(""), To: domain.StageLead})
	if err == nil {
		t.Fatal("событие без event_id не отвергнуто")
	}
	if !strings.Contains(err.Error(), "event_id") {
		t.Fatalf("ошибка не называет event_id: %v", err)
	}
}
