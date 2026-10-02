package transport_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	outboxinmemory "lead-gen/backend/contexts/engagement-observation/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/engagement-observation/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/engagement-observation/internal/adapters/transport"
	"lead-gen/backend/contexts/engagement-observation/internal/app/command"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/platform/messaging/inbox"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

type seqIDs struct{ n int }

func (s *seqIDs) NewEventID() domain.EventID {
	s.n++
	return domain.EventID(fmt.Sprintf("id-%d", s.n))
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestSubscriberQualifiesDeliveryEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	broker := amqptransport.NewInMemoryBroker(3)
	repo := persistence.New()
	ledger := persistence.NewLedger()
	links := persistence.NewLinks()
	journal := platformoutbox.NewMemoryJournal()
	uow := outboxinmemory.NewUnitOfWorkWith(repo, ledger, links, journal)
	ingest := command.NewIngestEventHandler(uow, fixedClock{t: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)}, &seqIDs{})
	sub := transport.NewSubscriber(broker, inbox.NewMemoryDeduper(), "", ingest)

	body, err := json.Marshal(map[string]string{
		"event_id":    "broker-1",
		"tenant_id":   "tenant-1",
		"company_id":  "company-1",
		"message_id":  "msg-1",
		"outcome":     "delivered",
		"occurred_at": "2026-01-05T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := broker.Publish(ctx, "message-delivery", "delivery-event.v1", body, map[string]string{"event_id": "broker-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		list, err := repo.ListByTenant(ctx, "tenant-1")
		if err == nil && len(list) == 1 {
			if list[0].Kind() != domain.KindDelivery || list[0].Outcome() != domain.OutcomeDelivered {
				t.Fatalf("квалификация = %q/%q", list[0].Kind(), list[0].Outcome())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("событие доставки не квалифицировано за отведённое время")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run не завершился по отмене контекста")
	}

	pending, err := journal.Pending(context.Background(), 10)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("записей outbox = %d, ожидалось 1", len(pending))
	}
}
