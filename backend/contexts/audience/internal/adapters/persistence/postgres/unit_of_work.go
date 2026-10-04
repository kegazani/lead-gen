package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	outboxinmemory "lead-gen/backend/contexts/audience/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// UnitOfWork — PostgreSQL-реализация ports.UnitOfWork.
//
// Транзакция открывается на пуле соединений и объединяет репозитории сегментов и
// снимков и outbox: запись агрегата и запись его событий выполняются в одной
// транзакции БД, поэтому «изменение сохранено, но событие не ушло» и «событие
// ушло, но изменение откатилось» невозможны. При ошибке fn транзакция
// откатывается и не сохраняет ни агрегат, ни события.
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
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("postgres: WithinTransaction без функции транзакции")
	}
	tx, err := u.begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: открытие транзакции: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
	// ошибка отката здесь намеренно игнорируется.
	defer func() { _ = tx.Rollback(ctx) }()

	t := &transaction{
		segments:  newSegmentTx(tx),
		snapshots: newSnapshotTx(tx),
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
	segments  *SegmentRepository
	snapshots *SnapshotRepository
	outbox    *transactionOutbox
}

func (t *transaction) Segments() ports.SegmentRepository           { return t.segments }
func (t *transaction) Snapshots() ports.AudienceSnapshotRepository { return t.snapshots }
func (t *transaction) Outbox() ports.Outbox                        { return t.outbox }

// transactionOutbox копит события до flush и записывает их в журнал,
// привязанный к той же транзакции. Кодирование формы записи общее с
// in-memory-реализацией: конверт события один на контекст.
type transactionOutbox struct {
	journal *Journal
	events  []domain.DomainEvent
}

// Append копит события до commit. Ни один метод не публикует события сам:
// перенос в журнал делает WithinTransaction после успешного завершения функции.
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
