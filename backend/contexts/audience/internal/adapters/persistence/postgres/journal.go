package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Journal — PostgreSQL-реализация platformoutbox.Journal поверх таблицы outbox.
//
// Один тип обслуживает и запись в транзакции (экземпляр, привязанный к pgx.Tx
// через newTxJournal), и публикацию relay (экземпляр поверх пула): форма записи
// одна, поэтому подтверждённое событие переживает рестарт процесса и не
// публикуется повторно. Append идемпотентен по ID (ON CONFLICT DO NOTHING):
// повторная запись той же записи не создаёт вторую.
type Journal struct {
	db execer
}

// Journal реализует журнал outbox платформы.
var _ platformoutbox.Journal = (*Journal)(nil)

// NewJournal создаёт журнал поверх пула соединений для чтения и подтверждения
// ожидающих записей.
func NewJournal(pool *pgxpool.Pool) *Journal {
	if pool == nil {
		panic("postgres: NewJournal с nil-пулом")
	}
	return &Journal{db: pool}
}

// newTxJournal создаёт журнал, привязанный к активной транзакции: записи,
// добавленные владельцем, коммитятся атомарно вместе с агрегатом.
func newTxJournal(tx execer) *Journal {
	return &Journal{db: tx}
}

// Append добавляет запись в журнал. Повторная запись с тем же ID — no-op.
func (j *Journal) Append(ctx context.Context, m platformoutbox.Message) error {
	if m.ID == "" {
		return errors.New("postgres: запись outbox без ID")
	}
	headers, err := json.Marshal(m.Headers)
	if err != nil {
		return fmt.Errorf("postgres: сериализация заголовков записи %s: %w", m.ID, err)
	}
	if _, err := j.db.Exec(ctx, insertOutboxMessage, m.ID, m.Exchange, m.RoutingKey, m.Body, string(headers)); err != nil {
		return fmt.Errorf("postgres: запись outbox %s: %w", m.ID, err)
	}
	return nil
}

// Pending возвращает ещё не опубликованные записи в порядке записи. limit больше
// нуля ограничивает размер порции; limit <= 0 возвращает все ожидающие.
func (j *Journal) Pending(ctx context.Context, limit int) ([]platformoutbox.Message, error) {
	var lim any
	if limit > 0 {
		lim = limit
	}
	rows, err := j.db.Query(ctx, selectPendingOutbox, lim)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение ожидающих записей outbox: %w", err)
	}
	defer rows.Close()

	messages := make([]platformoutbox.Message, 0)
	for rows.Next() {
		var (
			id, exchange, routingKey string
			body                     []byte
			headers                  []byte
		)
		if err := rows.Scan(&id, &exchange, &routingKey, &body, &headers); err != nil {
			return nil, fmt.Errorf("postgres: разбор записи outbox: %w", err)
		}
		decoded := map[string]string{}
		if len(headers) > 0 {
			if err := json.Unmarshal(headers, &decoded); err != nil {
				return nil, fmt.Errorf("postgres: разбор заголовков записи %s: %w", id, err)
			}
		}
		messages = append(messages, platformoutbox.Message{
			ID:         id,
			Exchange:   exchange,
			RoutingKey: routingKey,
			Body:       body,
			Headers:    decoded,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение ожидающих записей outbox: %w", err)
	}
	return messages, nil
}

// MarkPublished подтверждает успешную публикацию записи. Неизвестный ID —
// ошибка: подтверждать нечего, и молчаливый no-op скрыл бы рассинхронизацию.
func (j *Journal) MarkPublished(ctx context.Context, id string) error {
	tag, err := j.db.Exec(ctx, markOutboxPublished, id)
	if err != nil {
		return fmt.Errorf("postgres: подтверждение записи outbox %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: подтверждение неизвестной записи outbox %q", id)
	}
	return nil
}

// SQL-запросы журнала outbox.
const (
	insertOutboxMessage = `
INSERT INTO "audience".outbox (id, exchange, routing_key, body, headers)
VALUES ($1, $2, $3, $4, $5::jsonb)
ON CONFLICT (id) DO NOTHING`

	selectPendingOutbox = `
SELECT id, exchange, routing_key, body, headers::text
FROM "audience".outbox
WHERE published_at IS NULL
ORDER BY created_at ASC, id ASC
LIMIT $1`

	markOutboxPublished = `
UPDATE "audience".outbox
SET published_at = now()
WHERE id = $1`
)
