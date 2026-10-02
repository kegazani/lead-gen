package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

func deliveryEvent(t *testing.T, id domain.EngagementEventID) *domain.EngagementEvent {
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

func TestRepositorySaveFindList(t *testing.T) {
	ctx := context.Background()
	repo := New()
	event := deliveryEvent(t, "evt-1")

	if err := repo.Save(ctx, event, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Save(ctx, event, 0); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("повторное создание: ошибка = %v, ожидалась ErrVersionConflict", err)
	}
	if err := repo.Save(ctx, event, 1); err != nil {
		t.Fatalf("Save по версии 1: %v", err)
	}

	found, err := repo.FindByID(ctx, "evt-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.CompanyID() != "company-1" {
		t.Fatalf("company = %q", found.CompanyID())
	}
	if _, err := repo.FindByID(ctx, "missing"); !errors.Is(err, ports.ErrEventNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrEventNotFound", err)
	}

	list, err := repo.ListByTenant(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("событий = %d, ожидалось 1", len(list))
	}
}

func TestRepositoryCloneReplaceIsolation(t *testing.T) {
	ctx := context.Background()
	repo := New()
	if err := repo.Save(ctx, deliveryEvent(t, "evt-1"), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	clone := repo.Clone()
	if err := clone.Save(ctx, deliveryEvent(t, "evt-2"), 0); err != nil {
		t.Fatalf("Save в копию: %v", err)
	}
	if _, err := repo.FindByID(ctx, "evt-2"); !errors.Is(err, ports.ErrEventNotFound) {
		t.Fatalf("оригинал не должен видеть событие копии: %v", err)
	}

	repo.ReplaceWith(clone)
	if _, err := repo.FindByID(ctx, "evt-2"); err != nil {
		t.Fatalf("после commit событие копии должно быть видно: %v", err)
	}
}

func TestLedgerDedup(t *testing.T) {
	ctx := context.Background()
	ledger := NewLedger()
	seen, err := ledger.Seen(ctx, "signal:sig-1")
	if err != nil || seen {
		t.Fatalf("Seen до Mark = %v, %v", seen, err)
	}
	if err := ledger.Mark(ctx, "signal:sig-1"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	seen, err = ledger.Seen(ctx, "signal:sig-1")
	if err != nil || !seen {
		t.Fatalf("Seen после Mark = %v, %v", seen, err)
	}
	if err := ledger.Mark(ctx, ""); err == nil {
		t.Fatalf("Mark пустого ключа должен падать")
	}
}

func TestLinksResolve(t *testing.T) {
	ctx := context.Background()
	links := NewLinks()
	link, err := domain.NewTransitionLink("lnk-1", "https://example.com", "tenant-1", "company-1")
	if err != nil {
		t.Fatalf("NewTransitionLink: %v", err)
	}
	if err := links.Save(ctx, link); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := links.Resolve(ctx, "lnk-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Target != "https://example.com" || got.TenantID != "tenant-1" {
		t.Fatalf("ссылка = %+v", got)
	}
	if _, err := links.Resolve(ctx, "missing"); !errors.Is(err, ports.ErrLinkNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrLinkNotFound", err)
	}
}
