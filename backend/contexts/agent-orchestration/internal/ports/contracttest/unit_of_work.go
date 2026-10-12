package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWorkHarness — зависимости contract suite транзакции: unit-of-work и
// репозиторий/журнал, разделяющие с ним состояние.
type UnitOfWorkHarness struct {
	UnitOfWork ports.UnitOfWork
	Runs       ports.RunRepository
	Journal    platformoutbox.Journal
}

// RunUnitOfWorkContract проверяет контракт транзакции: commit фиксирует агрегат и
// его события вместе, ошибка use case откатывает обе записи.
func RunUnitOfWorkContract(t *testing.T, factory func(t *testing.T) UnitOfWorkHarness) {
	t.Helper()
	ctx := context.Background()

	t.Run("commit фиксирует агрегат и события", func(t *testing.T) {
		h := factory(t)
		run := NewRun(t, "run-commit", PublishedPipeline(t, "tenant-1", 1))
		if err := run.Start(baseTime, header("evt-start")); err != nil {
			t.Fatalf("Start: %v", err)
		}
		events := run.PullDomainEvents()
		if len(events) == 0 {
			t.Fatal("ожидалось хотя бы одно событие запуска")
		}
		if err := h.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Runs().Save(ctx, run, 0); err != nil {
				return err
			}
			return tx.Outbox().Append(ctx, events...)
		}); err != nil {
			t.Fatalf("WithinTransaction: %v", err)
		}
		if _, err := h.Runs.FindByID(ctx, run.ID(), "tenant-1"); err != nil {
			t.Fatalf("запуск не зафиксирован: %v", err)
		}
		pending, err := h.Journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != len(events) {
			t.Fatalf("записей outbox = %d, ожидалось %d", len(pending), len(events))
		}
	})

	t.Run("ошибка откатывает агрегат и события", func(t *testing.T) {
		h := factory(t)
		run := NewRun(t, "run-rollback", PublishedPipeline(t, "tenant-1", 1))
		if err := run.Start(baseTime, header("evt-start")); err != nil {
			t.Fatalf("Start: %v", err)
		}
		events := run.PullDomainEvents()
		wantErr := errors.New("сбой use case")
		err := h.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Runs().Save(ctx, run, 0); err != nil {
				return err
			}
			if err := tx.Outbox().Append(ctx, events...); err != nil {
				return err
			}
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("WithinTransaction вернул %v, ожидалась ошибка use case", err)
		}
		if _, err := h.Runs.FindByID(ctx, run.ID(), "tenant-1"); !errors.Is(err, ports.ErrRunNotFound) {
			t.Fatalf("откат оставил запуск: ошибка = %v", err)
		}
		if pending, err := h.Journal.Pending(ctx, 0); err != nil || len(pending) != 0 {
			t.Fatalf("откат оставил события outbox: %d (err=%v)", len(pending), err)
		}
	})
}
