package postgres

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Journal — PostgreSQL-журнал outbox: ожидающие публикации доменные события в
// таблице outbox схемы сервиса.
//
// Один тип обслуживает и запись в транзакции (экземпляр, привязанный к pgx.Tx
// через newTxJournal), и публикацию relay (экземпляр поверх пула): форма записи
// одна, поэтому подтверждённое событие переживает рестарт процесса и не
// публикуется повторно. Запись идёт в той же транзакции, что и агрегат
// (unit_of_work.go), поэтому «изменение сохранено, но событие не ушло» и
// «событие ушло, но изменение откатилось» невозможны.
type Journal struct {
	db execer
}

// Journal реализует источник публикации relay.
var _ relay.Source = (*Journal)(nil)

// NewJournal создаёт журнал поверх пула соединений для чтения и подтверждения
// ожидающих записей.
func NewJournal(pool *pgxpool.Pool) *Journal {
	if pool == nil {
		panic("postgres: NewJournal с nil-пулом")
	}
	return &Journal{db: pool}
}

// newTxJournal создаёт журнал, привязанный к активной транзакции: добавленные
// события коммитятся атомарно вместе с агрегатом.
func newTxJournal(tx execer) *Journal {
	return &Journal{db: tx}
}

// appendEvent кодирует событие и записывает его в outbox. Вызывается только в
// рамках активной транзакции unit-of-work: до commit запись не видна relay.
func (j *Journal) appendEvent(ctx context.Context, event domain.DomainEvent) error {
	name, payload, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if _, err := j.db.Exec(ctx, insertOutboxEvent, name, string(payload)); err != nil {
		return fmt.Errorf("postgres: запись события %s в outbox: %w", name, err)
	}
	return nil
}

// Pending возвращает неподтверждённые сообщения в порядке записи. Каждая запись
// декодируется в доменное событие; повреждённая запись — ошибка, а не пропуск:
// потерять событие молча нельзя.
func (j *Journal) Pending(ctx context.Context) ([]relay.Message, error) {
	rows, err := j.db.Query(ctx, selectPendingEvents)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение ожидающих событий outbox: %w", err)
	}
	defer rows.Close()

	messages := make([]relay.Message, 0)
	for rows.Next() {
		var (
			id      int64
			name    string
			payload []byte
		)
		if err := rows.Scan(&id, &name, &payload); err != nil {
			return nil, fmt.Errorf("postgres: разбор записи outbox: %w", err)
		}
		event, err := decodeEvent(name, payload)
		if err != nil {
			return nil, err
		}
		messages = append(messages, relay.Message{ID: strconv.FormatInt(id, 10), Event: event})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение ожидающих событий outbox: %w", err)
	}
	return messages, nil
}

// MarkPublished подтверждает публикацию записей по их идентификаторам.
// Подтверждение идемпотентно: неизвестный идентификатор не является ошибкой —
// так же ведёт себя in-memory-журнал, и relay вправе повторить подтверждение.
func (j *Journal) MarkPublished(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		key, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return fmt.Errorf("postgres: некорректный идентификатор записи outbox %q: %w", id, err)
		}
		if _, err := j.db.Exec(ctx, markOutboxPublished, key); err != nil {
			return fmt.Errorf("postgres: подтверждение записи outbox %s: %w", id, err)
		}
	}
	return nil
}

// SQL-запросы журнала outbox. Схема указана литералом: адаптер работает только в
// схеме сервиса (граница данных — schema-per-service).
const (
	insertOutboxEvent = `
INSERT INTO "outreach-orchestration".outbox (event_name, payload)
VALUES ($1, $2::jsonb)`

	selectPendingEvents = `
SELECT id, event_name, payload
FROM "outreach-orchestration".outbox
WHERE published_at IS NULL
ORDER BY id ASC`

	markOutboxPublished = `
UPDATE "outreach-orchestration".outbox
SET published_at = now()
WHERE id = $1`
)
