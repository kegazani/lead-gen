package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWorkHarness — доступ к портам одной транзакции вместе с хранилищем, где
// проверяется её исход: репозитории читают сохранённые агрегаты, журнал — записи
// outbox. Фабрика обязана вернуть harness с общим пустым состоянием.
type UnitOfWorkHarness struct {
	UnitOfWork ports.UnitOfWork
	Content    ports.ContentPackageRepository
	RenderJobs ports.RenderJobRepository
	Journal    platformoutbox.Journal
}

// UnitOfWorkFactory создаёт harness для одного прогона suite.
type UnitOfWorkFactory func(t *testing.T) UnitOfWorkHarness

// RunUnitOfWorkContract проверяет, что реализация UnitOfWork соблюдает контракт
// транзакции: запись агрегатов и их событий фиксируется атомарно в одной
// транзакции, а ошибка use case откатывает и агрегаты, и события.
func RunUnitOfWorkContract(t *testing.T, newHarness UnitOfWorkFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("коммит сохраняет агрегат и событие", func(t *testing.T) {
		harness := newHarness(t)
		pkg := mustPackage(t, "asset-commit")
		events := pkg.DomainEvents()

		err := harness.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Packages().Save(ctx, pkg, 0); err != nil {
				return err
			}
			return tx.Outbox().Append(ctx, events...)
		})
		if err != nil {
			t.Fatalf("WithinTransaction: %v", err)
		}

		loaded, err := harness.Content.FindByID(ctx, pkg.ID())
		if err != nil {
			t.Fatalf("FindByID после commit: %v", err)
		}
		assertPackage(t, loaded, pkg)

		pending, err := harness.Journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("ожидающих записей outbox = %d, ожидалась 1", len(pending))
		}
		if pending[0].ID != string(events[0].Header().EventID) {
			t.Fatalf("ID записи outbox = %q, ожидался event_id пакета", pending[0].ID)
		}
	})

	t.Run("откат не сохраняет ни агрегат, ни событие", func(t *testing.T) {
		harness := newHarness(t)
		pkg := mustPackage(t, "asset-rollback")
		sentinel := errors.New("сбой use case")

		err := harness.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Packages().Save(ctx, pkg, 0); err != nil {
				return err
			}
			if err := tx.Outbox().Append(ctx, pkg.DomainEvents()...); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("WithinTransaction вернул %v, ожидалась ошибка use case", err)
		}

		if _, err := harness.Content.FindByID(ctx, pkg.ID()); !errors.Is(err, ports.ErrContentPackageNotFound) {
			t.Fatalf("откат оставил пакет: ошибка = %v", err)
		}
		pending, err := harness.Journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("откат оставил %d записей outbox, ожидалось 0", len(pending))
		}
	})

	t.Run("пакет и задание сохраняются в одной транзакции", func(t *testing.T) {
		harness := newHarness(t)
		pkg := mustPackage(t, "asset-together")
		job := mustJob(t, "job-together", "requested")

		err := harness.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Packages().Save(ctx, pkg, 0); err != nil {
				return err
			}
			return tx.RenderJobs().Save(ctx, job, 0)
		})
		if err != nil {
			t.Fatalf("WithinTransaction: %v", err)
		}

		if _, err := harness.Content.FindByID(ctx, pkg.ID()); err != nil {
			t.Fatalf("пакет не сохранён: %v", err)
		}
		if _, err := harness.RenderJobs.FindByID(ctx, job.ID()); err != nil {
			t.Fatalf("задание не сохранено: %v", err)
		}
	})
}
