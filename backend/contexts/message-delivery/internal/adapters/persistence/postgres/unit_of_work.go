package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// UnitOfWork — PostgreSQL-реализация ports.UnitOfWork.
//
// Транзакция открывается на пуле соединений и объединяет репозитории и outbox:
// запись агрегата и запись его событий выполняются в одной транзакции БД, поэтому
// «изменение сохранено, но событие не ушло» и «событие ушло, но изменение
// откатилось» невозможны. При ошибке fn транзакция откатывается и не сохраняет
// ни агрегат, ни события.
type UnitOfWork struct {
	begin func(context.Context) (pgx.Tx, error)
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)
var _ ports.CampaignCanceller = (*UnitOfWork)(nil)

// NewUnitOfWork создаёт unit-of-work поверх пула соединений.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	if pool == nil {
		panic("postgres: NewUnitOfWork с nil-пулом")
	}
	return &UnitOfWork{begin: pool.Begin}
}

// CancelCampaign атомарно сохраняет tombstone кампании в границах тенанта и
// отменяет только работу, для которой отправка ещё не начиналась.
func (u *UnitOfWork) CancelCampaign(ctx context.Context, tenantID, campaignID string) error {
	tx, err := u.begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: отмена кампании: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockCampaign(ctx, tx, tenantID, campaignID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO "message-delivery".campaign_cancellation (tenant_id, campaign_id)
		VALUES ($1,$2) ON CONFLICT (tenant_id,campaign_id) DO NOTHING`, tenantID, campaignID); err != nil {
		return fmt.Errorf("postgres: запись tombstone отмены: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE "message-delivery".approved_action_inbox
		SET status='cancelled', lease_token=NULL, lease_until=NULL
		WHERE status IN ('pending','leased') AND payload->>'tenant_id'=$1 AND payload->>'campaign_id'=$2`, tenantID, campaignID); err != nil {
		return fmt.Errorf("postgres: fencing inbox отменённой кампании: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit отмены кампании: %w", err)
	}
	return nil
}

// WithinTransaction выполняет fn в одной транзакции БД.
//
// События, накопленные в буфере транзакции, flush-ятся в таблицу outbox до
// commit, поэтому фиксируются атомарно с агрегатом. Ошибка fn или запись outbox
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
		messages:    NewMessageRepositoryTx(tx),
		reputations: NewSenderReputationRepositoryTx(tx),
		outbox:      &transactionOutbox{journal: newTxJournal(tx)},
		db:          tx,
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
	messages    *Messages
	reputations *Reputations
	outbox      *transactionOutbox
	db          pgx.Tx
}

func (t *transaction) CampaignCancelled(ctx context.Context, tenantID, campaignID string) (bool, error) {
	if err := lockCampaign(ctx, t.db, tenantID, campaignID); err != nil {
		return false, err
	}
	var cancelled bool
	err := t.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM "message-delivery".campaign_cancellation
		WHERE tenant_id=$1 AND campaign_id=$2)`, tenantID, campaignID).Scan(&cancelled)
	if err != nil {
		return false, fmt.Errorf("postgres: чтение cancellation fence: %w", err)
	}
	return cancelled, nil
}

func lockCampaign(ctx context.Context, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, tenantID, campaignID string) error {
	_, err := db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array($1::text,$2::text)::text, 0))`, tenantID, campaignID)
	if err != nil {
		return fmt.Errorf("postgres: блокировка кампании: %w", err)
	}
	return nil
}

func (t *transaction) Messages() ports.MessageRepository { return t.messages }

func (t *transaction) Reputations() ports.SenderReputationRepository { return t.reputations }

func (t *transaction) Outbox() ports.Outbox { return t.outbox }

// transactionOutbox копит события до flush и записывает их в журнал,
// привязанный к той же транзакции.
type transactionOutbox struct {
	journal *Journal
	events  []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

// flush переносит накопленные события в журнал outbox в порядке поступления.
func (o *transactionOutbox) flush(ctx context.Context) error {
	for _, event := range o.events {
		if err := o.journal.appendEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
