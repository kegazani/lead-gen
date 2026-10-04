package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// UnitOfWork — PostgreSQL-реализация транзакции контекста: репозиторий
// тенантов, outbox и ключи идемпотентности записываются в одной транзакции БД.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork создаёт транзакцию поверх пула соединений.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	if pool == nil {
		panic("postgres: NewUnitOfWork с nil-пулом")
	}
	return &UnitOfWork{pool: pool}
}

// WithinTransaction выполняет fn в одной транзакции БД. Ошибка fn ведёт к
// откату: ни агрегат, ни события, ни ключ идемпотентности не сохраняются.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return fmt.Errorf("postgres: WithinTransaction без функции транзакции")
	}
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: открытие транзакции: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
	// ошибка отката здесь намеренно игнорируется.
	defer func() { _ = tx.Rollback(ctx) }()

	facade := &transaction{
		repo:   NewTx(tx),
		outbox: &outbox{db: tx},
		idem:   &idempotency{db: tx},
	}
	if err := fn(facade); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: коммит транзакции: %w", err)
	}
	return nil
}

// transaction — фасад над транзакцией БД.
type transaction struct {
	repo   *Repository
	outbox *outbox
	idem   *idempotency
}

func (t *transaction) Tenants() ports.TenantRepository { return t.repo }
func (t *transaction) Outbox() ports.Outbox            { return t.outbox }
func (t *transaction) Idempotency() ports.Idempotency  { return t.idem }

// outbox — transactional outbox поверх таблицы схемы сервиса.
type outbox struct {
	db execer
}

// Append записывает сообщения в outbox текущей транзакции. Сериализация
// полезной нагрузки в jsonb — забота адаптера, а не прикладного слоя.
func (o *outbox) Append(ctx context.Context, messages ...ports.Message) error {
	for _, message := range messages {
		payload, err := jsonPayload(message.Payload)
		if err != nil {
			return fmt.Errorf("postgres: сериализация сообщения %s: %w", message.Contract, err)
		}
		if _, err := o.db.Exec(ctx, insertOutbox,
			message.Contract+":"+message.EventID.String(),
			message.Contract,
			message.SchemaVersion,
			message.EventID.String(),
			message.TenantID.String(),
			message.OccurredAt,
			int64(message.SnapshotVersion),
			message.CorrelationID,
			message.CausationID,
			payload,
		); err != nil {
			return fmt.Errorf("postgres: запись сообщения %s: %w", message.Contract, err)
		}
	}
	return nil
}

// idempotency — ключи команд поверх таблицы схемы сервиса.
type idempotency struct {
	db execer
}

// Seen сообщает, применён ли ключ команды.
func (i *idempotency) Seen(ctx context.Context, key ports.CommandKey) (bool, error) {
	var exists bool
	if err := i.db.QueryRow(ctx, selectCommandKey, key.TenantID.String(), key.CommandID.String()).Scan(&exists); err != nil {
		return false, fmt.Errorf("postgres: проверка ключа команды: %w", err)
	}
	return exists, nil
}

// Remember фиксирует ключ команды в текущей транзакции.
func (i *idempotency) Remember(ctx context.Context, key ports.CommandKey) error {
	if _, err := i.db.Exec(ctx, insertCommandKey, key.TenantID.String(), key.CommandID.String()); err != nil {
		return fmt.Errorf("postgres: запись ключа команды: %w", err)
	}
	return nil
}

// Проверка формы execer у транзакции драйвера на этапе компиляции.
var _ execer = (pgx.Tx)(nil)

// SQL-запросы outbox и ключей команд.
const (
	insertOutbox = `
INSERT INTO "platform-governance".outbox_message
    (message_id, contract, schema_version, event_id, tenant_id, occurred_at, snapshot_version, correlation_id, causation_id, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (message_id) DO NOTHING`

	selectCommandKey = `
SELECT EXISTS (
    SELECT 1 FROM "platform-governance".command_key
    WHERE tenant_id = $1 AND command_id = $2
)`

	insertCommandKey = `
INSERT INTO "platform-governance".command_key (tenant_id, command_id)
VALUES ($1, $2)
ON CONFLICT (tenant_id, command_id) DO NOTHING`
)
