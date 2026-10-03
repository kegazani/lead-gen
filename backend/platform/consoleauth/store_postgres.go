package consoleauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema — имя схемы, которой принадлежит общая таблица сессий консоли. Схема
// одна на все консольные сервисы: один оператор и одна сессия, принятая любым из
// них. Совпадает с идентификатором пакета и шапкой миграции.
const Schema = "consoleauth"

// PostgresStore — стор сессий в общей базе консольных сервисов. Сессия, выданная
// одним процессом (и сохранённая здесь), принимается любым другим процессом,
// подключённым к той же базе.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// Проверка соответствия интерфейсу на этапе компиляции.
var _ Store = (*PostgresStore)(nil)

// NewPostgresStore создаёт стор поверх пула соединений. Пул общий с другими
// адаптерами сервиса: стор не владеет его закрытием.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		panic("consoleauth: NewPostgresStore с nil-пулом")
	}
	return &PostgresStore{pool: pool}
}

// Save записывает сессию, заменяя прежнюю запись с тем же ID.
func (s *PostgresStore) Save(ctx context.Context, rec Record) error {
	if _, err := s.pool.Exec(ctx, saveSessionSQL,
		rec.ID, rec.Username, rec.CSRF, rec.LastActive); err != nil {
		return fmt.Errorf("consoleauth: сохранение сессии %s: %w", rec.ID, err)
	}
	return nil
}

// Load возвращает сессию по ID; ok=false, если её нет (отсутствие записи — не
// ошибка).
func (s *PostgresStore) Load(ctx context.Context, id string) (Record, bool, error) {
	var rec Record
	err := s.pool.QueryRow(ctx, loadSessionSQL, id).
		Scan(&rec.ID, &rec.Username, &rec.CSRF, &rec.LastActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("consoleauth: чтение сессии %s: %w", id, err)
	}
	return rec, true, nil
}

// Delete удаляет сессию. Операция идемпотентна.
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	if _, err := s.pool.Exec(ctx, deleteSessionSQL, id); err != nil {
		return fmt.Errorf("consoleauth: удаление сессии %s: %w", id, err)
	}
	return nil
}

// SQL-запросы стора. Таблица создаётся миграцией consoleauth и лежит в её схеме.
const (
	saveSessionSQL = `
INSERT INTO consoleauth.console_sessions (id, username, csrf, last_active)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
SET username = EXCLUDED.username,
    csrf = EXCLUDED.csrf,
    last_active = EXCLUDED.last_active`

	loadSessionSQL = `
SELECT id, username, csrf, last_active
FROM consoleauth.console_sessions
WHERE id = $1`

	deleteSessionSQL = `
DELETE FROM consoleauth.console_sessions
WHERE id = $1`
)
