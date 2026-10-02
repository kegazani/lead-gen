package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/content-production/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

func buildPackage(t *testing.T) *domain.ContentPackage {
	t.Helper()
	locale, err := domain.NewContentLocale("ru", "")
	if err != nil {
		t.Fatalf("NewContentLocale: %v", err)
	}
	pkg, err := domain.NewContentPackage(domain.NewContentPackageParams{
		ID:        "asset-1",
		TenantID:  "tenant-1",
		Locale:    locale,
		VersionID: "ver-1",
		Body:      "Текст.",
		Actor:     "actor-1",
		At:        time.Unix(1000, 0).UTC(),
		EventID:   "evt-1",
	})
	if err != nil {
		t.Fatalf("NewContentPackage: %v", err)
	}
	return pkg
}

func TestWithinTransactionCommitsAggregateAndEvents(t *testing.T) {
	ctx := context.Background()
	store := persistence.New()
	journal := platformoutbox.NewMemoryJournal()
	uow := NewUnitOfWorkWith(store, journal)

	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		pkg := buildPackage(t)
		if err := tx.Packages().Save(ctx, pkg, 0); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, pkg.DomainEvents()...)
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}
	if _, err := store.Content().FindByID(ctx, "asset-1"); err != nil {
		t.Fatalf("агрегат не сохранён: %v", err)
	}
	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("событий в outbox: %d, ожидалось 1", len(pending))
	}
	if pending[0].ID != "evt-1" || pending[0].Exchange != "content-production" {
		t.Fatalf("запись outbox неполна: %+v", pending[0])
	}
	if pending[0].RoutingKey != "content-version-created.v1" {
		t.Fatalf("ключ маршрутизации: %q", pending[0].RoutingKey)
	}
}

func TestWithinTransactionRollsBack(t *testing.T) {
	ctx := context.Background()
	store := persistence.New()
	journal := platformoutbox.NewMemoryJournal()
	uow := NewUnitOfWorkWith(store, journal)

	sentinel := errors.New("boom")
	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Packages().Save(ctx, buildPackage(t), 0); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, buildPackage(t).DomainEvents()...); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ожидалась sentinel-ошибка, получено %v", err)
	}
	if _, err := store.Content().FindByID(ctx, "asset-1"); !errors.Is(err, ports.ErrContentPackageNotFound) {
		t.Fatalf("откат не убрал агрегат: %v", err)
	}
	pending, _ := journal.Pending(ctx, 0)
	if len(pending) != 0 {
		t.Fatalf("откат оставил события: %d", len(pending))
	}
}
