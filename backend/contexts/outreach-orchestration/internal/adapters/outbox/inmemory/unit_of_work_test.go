package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

var testNow = time.Date(2026, time.February, 3, 9, 0, 0, 0, time.UTC)

// UnitOfWork реализует порт транзакции среза.
var _ ports.UnitOfWork = (*inmemory.UnitOfWork)(nil)

func newCampaign(t *testing.T) *domain.Campaign {
	t.Helper()
	window, err := domain.NewCampaignWindow(testNow, testNow.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-1",
		TenantID: "tenant-1",
		Name:     "Кампания outbox",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return campaign
}

func sampleEvent() domain.ActionApproved {
	return domain.ActionApproved{
		CampaignID:   "campaign-1",
		TenantID:     "tenant-1",
		DraftID:      "draft-1",
		DraftVersion: 1,
		DecisionID:   "decision-1",
		Actor:        "operator-1",
		At:           testNow,
	}
}

func TestUnitOfWorkCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uow := inmemory.NewUnitOfWork()

	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		// До commit журнал пуст: событие живёт только в буфере транзакции.
		if got := uow.Journal().PendingCount(); got != 0 {
			t.Fatalf("до commit в журнале %d записей, ожидалось 0", got)
		}
		if err := tx.Campaigns().Save(ctx, newCampaign(t)); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, sampleEvent())
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	if _, err := uow.Repository().FindByID(ctx, "campaign-1"); err != nil {
		t.Fatalf("после commit кампания не найдена: %v", err)
	}
	if got := uow.Journal().PendingCount(); got != 1 {
		t.Fatalf("после commit в журнале %d записей, ожидалась 1", got)
	}
}

func TestUnitOfWorkRollbackDropsBoth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uow := inmemory.NewUnitOfWork()
	boom := errors.New("сбой use case")

	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Campaigns().Save(ctx, newCampaign(t)); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, sampleEvent()); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithinTransaction вернул %v, ожидалась ошибка use case", err)
	}

	if _, err := uow.Repository().FindByID(ctx, "campaign-1"); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Fatalf("после отката кампания видна: %v", err)
	}
	if got := uow.Journal().PendingCount(); got != 0 {
		t.Fatalf("после отката в журнале %d записей, ожидалось 0", got)
	}
}

func TestUnitOfWorkOnlyCommitsEventsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uow := inmemory.NewUnitOfWork()

	appendEvent := func() error {
		return uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
			return tx.Outbox().Append(ctx, sampleEvent())
		})
	}
	if err := appendEvent(); err != nil {
		t.Fatalf("первый commit: %v", err)
	}
	if err := appendEvent(); err != nil {
		t.Fatalf("второй commit: %v", err)
	}
	if got := uow.Journal().PendingCount(); got != 2 {
		t.Fatalf("в журнале %d записей, ожидалось 2", got)
	}
}
