package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	outboxinmemory "lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// UnitOfWork — PostgreSQL-реализация ports.UnitOfWork.
//
// Транзакция открывается на пуле соединений и объединяет репозиторий заявок и
// outbox: запись агрегата и запись его событий выполняются в одной транзакции
// БД, поэтому «изменение сохранено, но событие не ушло» и «событие ушло, но
// изменение откатилось» невозможны. При ошибке fn транзакция откатывается и не
// сохраняет ни агрегат, ни события.
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
//
// События, накопленные в буфере транзакции, flush-ятся в таблицу outbox до
// commit, поэтому они фиксируются атомарно с агрегатом. Ошибка fn или запись
// outbox ведёт к откату.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(txCtx context.Context, tx ports.Transaction) error) error {
	if ports.HasTransactionScope(ctx) {
		return ports.ErrNestedTransaction
	}
	if fn == nil {
		return errors.New("postgres: WithinTransaction без функции транзакции")
	}
	tx, err := u.begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: открытие транзакции: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
	// ошибка отката здесь намеренно игнорируется.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	t := &transaction{requests: NewTx(tx), attempts: NewExecutionAttemptRepository(tx), resolutions: NewExecutionResolutionRepository(tx), outbox: &transactionOutbox{journal: newTxJournal(tx)}}
	txCtx := ports.WithTransactionScope(ctx)
	if err := txCtx.Err(); err != nil {
		return err
	}
	if err := fn(txCtx, t); err != nil {
		return err
	}
	if err := t.outbox.flush(txCtx); err != nil {
		return err
	}
	if err := txCtx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return fmt.Errorf("postgres: коммит транзакции: %w", err)
	}
	return nil
}

// transaction — фасад над транзакцией: репозиторий и outbox, привязанные к ней.
type transaction struct {
	requests    *Repository
	attempts    *ExecutionAttemptRepository
	resolutions *ExecutionResolutionRepository
	outbox      *transactionOutbox
}

func (t *transaction) Requests() ports.RequestRepository                   { return t.requests }
func (t *transaction) Outbox() ports.Outbox                                { return t.outbox }
func (t *transaction) ExecutionAttempts() ports.ExecutionAttemptRepository { return t.attempts }
func (t *transaction) ExecutionResolutions() ports.ExecutionResolutionRepository {
	return t.resolutions
}
func (t *transaction) VerifiedRuntimeResolutions() ports.VerifiedRuntimeResolutionWriter {
	return t.resolutions
}

// transactionOutbox копит события до flush и записывает их в журнал,
// привязанный к той же транзакции. Кодирование формы записи общее с
// in-memory-реализацией: конверт события один на контекст.
type transactionOutbox struct {
	journal *Journal
	events  []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

// flush переносит накопленные события в журнал outbox, кодируя каждое в запись с
// ключом идемпотентности event_id.
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
