package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWorkHarness — доступ к портам одной транзакции вместе с хранилищем, где
// проверяется её исход: репозиторий читает сохранённый агрегат, журнал — запись
// outbox. Фабрика обязана вернуть harness с общим пустым состоянием.
type UnitOfWorkHarness struct {
	UnitOfWork ports.UnitOfWork
	Repository ports.RequestRepository
	Journal    platformoutbox.Journal
}

// UnitOfWorkFactory создаёт harness для одного прогона suite.
type UnitOfWorkFactory func(t *testing.T) UnitOfWorkHarness

// RunUnitOfWorkContract проверяет, что реализация UnitOfWork соблюдает контракт
// транзакции: запись агрегата и его событий фиксируется атомарно, а ошибка
// use case откатывает и агрегат, и события.
func RunUnitOfWorkContract(t *testing.T, newHarness UnitOfWorkFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("коммит сохраняет агрегат и событие", func(t *testing.T) {
		harness := newHarness(t)
		request := mustRequest(t, "req-commit")
		events := request.DomainEvents()

		err := harness.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Requests().Save(ctx, request, 0); err != nil {
				return err
			}
			return tx.Outbox().Append(ctx, events...)
		})
		if err != nil {
			t.Fatalf("WithinTransaction: %v", err)
		}

		loaded, err := harness.Repository.FindByID(ctx, request.ID())
		if err != nil {
			t.Fatalf("FindByID после commit: %v", err)
		}
		assertRequest(t, loaded, request)

		pending, err := harness.Journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("ожидающих записей outbox = %d, ожидалась 1", len(pending))
		}
		if pending[0].ID != string(events[0].Header().EventID) {
			t.Fatalf("ID записи outbox = %q, ожидался event_id заявки", pending[0].ID)
		}
	})

	t.Run("откат не сохраняет ни агрегат, ни событие", func(t *testing.T) {
		harness := newHarness(t)
		request := mustRequest(t, "req-rollback")
		sentinel := errors.New("сбой use case")

		err := harness.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Requests().Save(ctx, request, 0); err != nil {
				return err
			}
			if err := tx.Outbox().Append(ctx, request.DomainEvents()...); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("WithinTransaction вернул %v, ожидалась ошибка use case", err)
		}

		if _, err := harness.Repository.FindByID(ctx, request.ID()); !errors.Is(err, ports.ErrRequestNotFound) {
			t.Fatalf("откат оставил заявку: ошибка = %v", err)
		}
		pending, err := harness.Journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("откат оставил %d записей outbox, ожидалось 0", len(pending))
		}
	})
}
