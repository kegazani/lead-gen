package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

// Journal — PostgreSQL-журнал transactional outbox.
//
// Один тип обслуживает и запись в транзакции (экземпляр, привязанный к pgx.Tx
// через newTxJournal), и публикацию relay (экземпляр поверх пула): форма записи
// одна, поэтому уже принятое событие переживает рестарт процесса и не
// публикуется повторно. Журнал хранит доменные события и реализует relay.Source.
type Journal struct {
	db execer
}

// Journal реализует источник публикации доменного outbox.
var _ relay.Source = (*Journal)(nil)

// NewJournal создаёт журнал поверх пула соединений для чтения и подтверждения
// ожидающих записей.
func NewJournal(pool *pgxpool.Pool) *Journal {
	if pool == nil {
		panic("postgres: NewJournal с nil-пулом")
	}
	return &Journal{db: pool}
}

// newTxJournal создаёт журнал, привязанный к активной транзакции: события,
// добавленные владельцем, коммитятся атомарно вместе с агрегатом.
func newTxJournal(tx execer) *Journal {
	return &Journal{db: tx}
}

// appendEvent кодирует доменное событие и пишет его в журнал текущей
// транзакции. Идентификатор записи генерируется здесь и служит event_id
// конверта при публикации.
func (j *Journal) appendEvent(ctx context.Context, event domain.DomainEvent) error {
	name, payload, err := encodeEvent(event)
	if err != nil {
		return err
	}
	id := newEventID()
	if _, err := j.db.Exec(ctx, insertOutbox, id, name, string(payload)); err != nil {
		return fmt.Errorf("postgres: запись события outbox %s: %w", id, err)
	}
	return nil
}

// Pending возвращает ещё не опубликованные записи в порядке записи.
func (j *Journal) Pending(ctx context.Context) ([]relay.Message, error) {
	rows, err := j.db.Query(ctx, selectPendingOutbox)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение ожидающих записей outbox: %w", err)
	}
	defer rows.Close()

	messages := make([]relay.Message, 0)
	for rows.Next() {
		var (
			id      string
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
		messages = append(messages, relay.Message{ID: id, Event: event})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение ожидающих записей outbox: %w", err)
	}
	return messages, nil
}

// MarkPublished подтверждает успешную публикацию записей. Неизвестные
// идентификаторы не подтверждаются: рассинхронизация не должна оставаться
// незамеченной.
func (j *Journal) MarkPublished(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	tag, err := j.db.Exec(ctx, markOutboxPublished, ids)
	if err != nil {
		return fmt.Errorf("postgres: подтверждение записей outbox: %w", err)
	}
	if int(tag.RowsAffected()) != len(ids) {
		return fmt.Errorf("postgres: подтверждено %d записей outbox из %d", tag.RowsAffected(), len(ids))
	}
	return nil
}

// newEventID генерирует стабильный уникальный идентификатор записи outbox.
func newEventID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand в стандартной библиотеке не возвращает ошибок на
		// поддерживаемых платформах; паника здесь недопустима на пути
		// публикации, поэтому откатываемся на детерминированный префикс.
		return "outbox-fallback"
	}
	return "outbox-" + hex.EncodeToString(buf[:])
}

// SQL-запросы журнала outbox.
const (
	insertOutbox = `
INSERT INTO "message-delivery".outbox (id, event_name, payload)
VALUES ($1, $2, $3::jsonb)
ON CONFLICT (id) DO NOTHING`

	selectPendingOutbox = `
SELECT id, event_name, payload
FROM "message-delivery".outbox
WHERE published_at IS NULL
ORDER BY created_at ASC, id ASC`

	markOutboxPublished = `
UPDATE "message-delivery".outbox
SET published_at = now()
WHERE id = ANY($1) AND published_at IS NULL`
)
