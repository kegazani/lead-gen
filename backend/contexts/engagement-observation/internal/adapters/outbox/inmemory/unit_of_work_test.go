package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/engagement-observation/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/contracts/engagement-observation/engagementevent"
)

func qualified(t *testing.T, id domain.EngagementEventID) *domain.EngagementEvent {
	t.Helper()
	event, err := domain.Qualify(domain.QualifyParams{
		ID: id,
		Signal: domain.RawSignal{
			SignalID: "sig-1", Source: "provider",
			TenantID: "tenant-1", CompanyID: "company-1",
			KindHint: "delivery", Outcome: "delivered",
			OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		EventID: "event-1",
	})
	if err != nil {
		t.Fatalf("Qualify: %v", err)
	}
	return event
}

func TestUnitOfWorkCommitsEventAndOutbox(t *testing.T) {
	ctx := context.Background()
	repo := persistence.New()
	ledger := persistence.NewLedger()
	links := persistence.NewLinks()
	journal := platformoutbox.NewMemoryJournal()
	uow := NewUnitOfWorkWith(repo, ledger, links, journal)

	event := qualified(t, "evt-1")
	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Engagements().Save(ctx, event, 0); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, event.DomainEvents()...); err != nil {
			return err
		}
		return tx.Signals().Mark(ctx, "signal:sig-1")
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	if _, err := repo.FindByID(ctx, "evt-1"); err != nil {
		t.Fatalf("событие не сохранено: %v", err)
	}
	seen, _ := ledger.Seen(ctx, "signal:sig-1")
	if !seen {
		t.Fatalf("отметка дедупликации не сохранена")
	}
	pending, err := journal.Pending(ctx, 10)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("записей outbox = %d, ожидалось 1", len(pending))
	}
	if pending[0].Exchange != "engagement-observation" {
		t.Fatalf("обменник = %q", pending[0].Exchange)
	}
	if pending[0].RoutingKey != "engagement-observed.v1" {
		t.Fatalf("ключ маршрутизации = %q", pending[0].RoutingKey)
	}
}

func TestUnitOfWorkRollsBack(t *testing.T) {
	ctx := context.Background()
	repo := persistence.New()
	ledger := persistence.NewLedger()
	links := persistence.NewLinks()
	journal := platformoutbox.NewMemoryJournal()
	uow := NewUnitOfWorkWith(repo, ledger, links, journal)

	event := qualified(t, "evt-1")
	wantErr := errors.New("отказ после записи")
	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Engagements().Save(ctx, event, 0); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, event.DomainEvents()...); err != nil {
			return err
		}
		if err := tx.Signals().Mark(ctx, "signal:sig-1"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ошибка = %v, ожидалась %v", err, wantErr)
	}

	if _, err := repo.FindByID(ctx, "evt-1"); !errors.Is(err, ports.ErrEventNotFound) {
		t.Fatalf("откат не убрал событие: %v", err)
	}
	seen, _ := ledger.Seen(ctx, "signal:sig-1")
	if seen {
		t.Fatalf("откат не убрал отметку дедупликации")
	}
	pending, _ := journal.Pending(ctx, 10)
	if len(pending) != 0 {
		t.Fatalf("откат не убрал записи outbox: %d", len(pending))
	}
}

func TestEncodeMessageBuildsContract(t *testing.T) {
	event := qualified(t, "evt-1")
	message, err := EncodeMessage(event.DomainEvents()[0])
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}
	if message.Headers["event_name"] != "engagement-observed" {
		t.Fatalf("event_name = %q", message.Headers["event_name"])
	}
	if message.Headers["schema_version"] != "v1" {
		t.Fatalf("schema_version = %q", message.Headers["schema_version"])
	}
	if _, err := EncodeMessage(nil); err == nil {
		t.Fatalf("кодирование nil-события должно падать")
	}
}

// TestEncodeRejectsPixelKind проверяет, что пиксельный вид не проходит и через
// контракт: Validate отвергает его, а не только домен.
func TestEncodeRejectsPixelKind(t *testing.T) {
	pixel := engagementevent.EngagementEvent{
		Envelope: engagementevent.Envelope{
			EventID: "event-1", OccurredAt: time.Now(), TenantID: "tenant-1",
			CorrelationID: "corr-1", Name: engagementevent.ContractName,
			SchemaVersion: engagementevent.SchemaVersion,
		},
		EngagementEventID: "evt-1", CompanyID: "company-1",
		Kind: "open", Confidence: "high", Outcome: "opened",
	}
	if err := pixel.Validate(); err == nil {
		t.Fatalf("пиксельный вид должен быть отвергнут контрактом")
	}
}
