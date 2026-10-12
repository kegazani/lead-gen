package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

type cancellationTestAccess struct{}

func (cancellationTestAccess) CanAct(context.Context, domain.ActorID, domain.TenantID) (bool, error) {
	return true, nil
}

type cancellationTestClock struct{}

func (cancellationTestClock) Now() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }

// TestRequestCancellationFencePostgres проверяет durable tombstone до handoff,
// idempotent replay и блокировку кампании после регистрации. Без opt-in DSN
// тест корректно пропускается и не является PostgreSQL acceptance.
func TestRequestCancellationFencePostgres(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, `DELETE FROM "outreach-orchestration".request_cancellation_fence WHERE tenant_id = 'fence-test'`); err != nil {
		t.Fatalf("очистка fence: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "outreach-orchestration".campaign WHERE tenant_id = 'fence-test'`); err != nil {
		t.Fatalf("очистка тестовых кампаний: %v", err)
	}
	uow := postgres.NewUnitOfWork(pool)
	for i := 0; i < 2; i++ {
		if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
			fenced, ok := tx.(ports.TransactionRequestCancellationFence)
			if !ok {
				t.Fatal("PostgreSQL transaction не предоставил request fence")
			}
			return fenced.RequestCancellationFence().Cancel(ctx, "fence-test", "request-before-handoff")
		}); err != nil {
			t.Fatalf("Cancel replay %d: %v", i, err)
		}
	}
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		fenced := tx.(ports.TransactionRequestCancellationFence).RequestCancellationFence()
		cancelled, err := fenced.RegisterHandoff(ctx, "fence-test", "request-before-handoff", "handoff-request-before-handoff")
		if err != nil {
			return err
		}
		if !cancelled {
			t.Fatal("сохранённая до handoff отмена не вернулась")
		}
		return nil
	}); err != nil {
		t.Fatalf("RegisterHandoff: %v", err)
	}
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		cancelled, err := tx.(ports.TransactionRequestCancellationFence).RequestCancellationFence().CampaignCancelled(ctx, "fence-test", "handoff-request-before-handoff")
		if err != nil {
			return err
		}
		if !cancelled {
			t.Fatal("поздняя проверка кампании потеряла tombstone")
		}
		return nil
	}); err != nil {
		t.Fatalf("CampaignCancelled: %v", err)
	}
	// Cancellation after a successful handoff remains visible to later approval
	// checks and late duplicate handoff delivery.
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		fence := tx.(ports.TransactionRequestCancellationFence).RequestCancellationFence()
		cancelled, err := fence.RegisterHandoff(ctx, "fence-test", "request-after-handoff", "handoff-request-after-handoff")
		if err != nil {
			return err
		}
		if cancelled {
			t.Fatal("новая заявка неожиданно отменена")
		}
		return nil
	}); err != nil {
		t.Fatalf("RegisterHandoff для второй заявки: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
			return tx.(ports.TransactionRequestCancellationFence).RequestCancellationFence().Cancel(ctx, "fence-test", "request-after-handoff")
		}); err != nil {
			t.Fatalf("Cancel after handoff replay %d: %v", i, err)
		}
	}
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		fence := tx.(ports.TransactionRequestCancellationFence).RequestCancellationFence()
		cancelled, err := fence.CampaignCancelled(ctx, "fence-test", "handoff-request-after-handoff")
		if err != nil {
			return err
		}
		if !cancelled {
			t.Fatal("отмена после handoff не блокирует approval")
		}
		cancelled, err = fence.RegisterHandoff(ctx, "fence-test", "request-after-handoff", "handoff-request-after-handoff")
		if err != nil {
			return err
		}
		if !cancelled {
			t.Fatal("поздний повтор handoff потерял fence")
		}
		return nil
	}); err != nil {
		t.Fatalf("проверка отмены после handoff: %v", err)
	}

	// Контрактный application path использует тот же PG UOW: сохранение handoff,
	// cancellation tombstone и отказ обоих вариантов approval выполняются по DB.
	appHandoff := command.NewSubmitHandoffHandler(cancellationTestAccess{}, cancellationTestClock{}, uow)
	result, err := appHandoff.Handle(ctx, command.SubmitHandoff{
		RequestID: "application-request", TenantID: "fence-test",
		Items: []command.SubmitHandoffItem{{LeadID: "lead", RecipientRef: "recipient", MaterialRef: "material"}},
	})
	if err != nil {
		t.Fatalf("application handoff: %v", err)
	}
	if err := command.NewCancelRequestHandler(uow).Handle(ctx, command.CancelRequest{TenantID: "fence-test", RequestID: "application-request"}); err != nil {
		t.Fatalf("application cancel: %v", err)
	}
	campaign, err := postgres.New(pool).FindByID(ctx, result.CampaignID)
	if err != nil {
		t.Fatalf("перечитать handoff campaign: %v", err)
	}
	draft := campaign.Drafts()[0]
	_, err = command.NewApproveDraftHandler(cancellationTestAccess{}, cancellationTestClock{}, uow).Handle(ctx, command.ApproveDraft{
		CampaignID: campaign.ID(), TenantID: campaign.TenantID(), Actor: "operator", DraftID: draft.ID(),
		DecisionID: "must-not-approve", ExpectedVersion: draft.Version(),
	})
	if err == nil || !errors.Is(err, command.ErrRequestCancelled) {
		t.Fatalf("PG single approval не заблокирован: %v", err)
	}
	_, err = command.NewApproveBatchHandler(cancellationTestAccess{}, cancellationTestClock{}, uow,
		func(context.Context, domain.RiskFactsRequest) ([]domain.RiskReason, error) { return nil, nil },
	).Handle(ctx, command.ApproveBatch{CampaignID: campaign.ID(), TenantID: campaign.TenantID(), Actor: "operator", Digest: "nonempty", Composition: []command.BatchApproveItem{{DraftID: draft.ID(), Version: draft.Version()}}})
	if err == nil || !errors.Is(err, command.ErrRequestCancelled) {
		t.Fatalf("PG batch approval не заблокирован: %v", err)
	}
}
