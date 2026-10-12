package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema — схема сервиса, которой принадлежит таблица сессий портала.
const Schema = "platform-governance"

// PostgresStore — стор сессий портала в PostgreSQL. Сессия, выданная одним
// процессом, принимается любым другим процессом, подключённым к той же базе.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// Проверка соответствия интерфейсу на этапе компиляции.
var _ Store = (*PostgresStore)(nil)

// NewPostgresStore создаёт стор поверх пула соединений. Пул общий с другими
// адаптерами сервиса: стор не владеет его закрытием.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		panic("session: NewPostgresStore с nil-пулом")
	}
	return &PostgresStore{pool: pool}
}

// Save записывает сессию, заменяя прежнюю запись с тем же ID.
func (s *PostgresStore) Save(ctx context.Context, rec Record) error {
	if _, err := s.pool.Exec(ctx, saveSessionSQL, rec.ID, rec.TenantID, rec.UserID, rec.LastActive); err != nil {
		return fmt.Errorf("session: сохранение сессии %s: %w", rec.ID, err)
	}
	return nil
}

// Load возвращает сессию по ID; ok=false, если её нет.
func (s *PostgresStore) Load(ctx context.Context, id string) (Record, bool, error) {
	var rec Record
	err := s.pool.QueryRow(ctx, loadSessionSQL, id).Scan(&rec.ID, &rec.TenantID, &rec.UserID, &rec.LastActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("session: чтение сессии %s: %w", id, err)
	}
	return rec, true, nil
}

// Delete удаляет сессию. Операция идемпотентна.
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	if _, err := s.pool.Exec(ctx, deleteSessionSQL, id); err != nil {
		return fmt.Errorf("session: удаление сессии %s: %w", id, err)
	}
	return nil
}

// SQL-запросы стора. Таблица создаётся миграцией platform-governance и лежит в
// её схеме.
const (
	saveSessionSQL = `
INSERT INTO "platform-governance".portal_session (id, tenant_id, user_id, last_active)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
SET tenant_id = EXCLUDED.tenant_id,
    user_id = EXCLUDED.user_id,
    last_active = EXCLUDED.last_active`

	loadSessionSQL = `
SELECT id, tenant_id, user_id, last_active
FROM "platform-governance".portal_session
WHERE id = $1`

	deleteSessionSQL = `
DELETE FROM "platform-governance".portal_session
WHERE id = $1`
)
