package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	outboxinmemory "lead-gen/backend/contexts/agent-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// UnitOfWork — PostgreSQL-реализация ports.UnitOfWork.
//
// Транзакция открывается на пуле соединений и объединяет репозитории пайплайнов и
// запусков и outbox: запись агрегата и запись его событий выполняются в одной
// транзакции БД, поэтому «изменение сохранено, но событие не ушло» и «событие ушло,
// но изменение откатилось» невозможны.
type UnitOfWork struct {
	begin func(context.Context) (pgx.Tx, error)
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork создаёт unit-of-work поверх пула соединений.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	if pool == nil {
		panic("postgres: NewUnitOfWork с nil-пулом")
	}
	return &UnitOfWork{begin: pool.Begin}
}

// WithinTransaction выполняет fn в одной транзакции БД.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("postgres: WithinTransaction без функции транзакции")
	}
	tx, err := u.begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: открытие транзакции: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	t := &transaction{
		pipelines: NewTxPipelineRepository(tx),
		runs:      NewTxRunRepository(tx),
		outbox:    &transactionOutbox{journal: newTxJournal(tx)},
	}
	if err := fn(t); err != nil {
		return err
	}
	if err := t.outbox.flush(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: коммит транзакции: %w", err)
	}
	return nil
}

// transaction — фасад над транзакцией: репозитории и outbox, привязанные к ней.
type transaction struct {
	pipelines *PipelineRepository
	runs      *RunRepository
	outbox    *transactionOutbox
}

func (t *transaction) Pipelines() ports.PipelineRepository { return t.pipelines }
func (t *transaction) Runs() ports.RunRepository           { return t.runs }
func (t *transaction) Outbox() ports.Outbox                { return t.outbox }

// transactionOutbox копит события до flush и записывает их в журнал, привязанный к
// той же транзакции. Кодирование формы записи общее с in-memory-реализацией:
// конверт события один на контекст.
type transactionOutbox struct {
	journal *Journal
	events  []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

// flush переносит накопленные события в журнал outbox.
func (o *transactionOutbox) flush(ctx context.Context) error {
	for _, event := range o.events {
		message, err := outboxinmemory.EncodeMessage(event)
		if err != nil {
			return err
		}
		if err := o.journal.Append(ctx, message); err != nil {
			return err
		}
	}
	return nil
}
