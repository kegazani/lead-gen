package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// UnitOfWork — PostgreSQL-реализация ports.UnitOfWork.
//
// Транзакция открывается на пуле соединений и объединяет репозиторий кампаний и
// outbox: запись агрегата и запись его событий выполняются в одной транзакции БД,
// поэтому «изменение сохранено, но событие не ушло» и «событие ушло, но изменение
// откатилось» невозможны. При ошибке функции транзакция откатывается и не
// сохраняет ни агрегат, ни события.
//
// Движок последовательностей (internal/app/scheduling) работает через этот же
// порт: сохранение агрегата и события прохода атомарны и на PostgreSQL, а не
// только на in-memory-реализации.
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
// Репозиторий и outbox, полученные из транзакции, привязаны к ней: Save не
// открывает вложенную транзакцию. Накопленные события flush-ятся в таблицу outbox
// до commit, поэтому фиксируются атомарно с агрегатом. Ошибка fn или запись outbox
// ведёт к откату.
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
		campaigns: NewTx(tx),
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

// transaction — фасад над транзакцией: репозиторий и outbox, привязанные к ней.
type transaction struct {
	campaigns *Repository
	outbox    *transactionOutbox
}

func (t *transaction) Campaigns() ports.CampaignRepository { return t.campaigns }
func (t *transaction) Outbox() ports.Outbox                { return t.outbox }

// transactionOutbox копит события до flush. Ни один метод не публикует события
// сам: перенос в журнал делает WithinTransaction до commit.
type transactionOutbox struct {
	journal *Journal
	events  []domain.DomainEvent
}

// Append копит события до commit.
func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

// flush записывает накопленные события в журнал outbox, привязанный к той же
// транзакции, кодируя каждое в проводимую форму.
func (o *transactionOutbox) flush(ctx context.Context) error {
	for _, event := range o.events {
		if err := o.journal.appendEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
