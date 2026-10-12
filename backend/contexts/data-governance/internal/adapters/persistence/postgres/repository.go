// Package postgres реализует ports.ProcessingBasisRepository поверх PostgreSQL.
//
// Адаптер хранит агрегат не указателем, а снимком
// (domain.ProcessingBasisSnapshot): Save пишет снимок, FindByID читает его и
// восстанавливает агрегат через domain.RehydrateProcessingBasis. Поэтому
// сохранённое состояние изолировано от вызывающего, а загрузка заново проверяет
// инварианты.
//
// Граница данных — schema-per-service: адаптер работает только в схеме
// data-governance (Schema) и не обращается к схемам других сервисов.
//
// # Транзакция
//
// Репозиторий получает пул соединений и для каждого Save открывает собственную
// транзакцию, чтобы составная запись (заголовок основания и подчинённые записи
// стоп-листа) была атомарной.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "data-governance"

// execer — минимальная поверхность драйвера, нужная репозиторию. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

// Repository — PostgreSQL-реализация ports.ProcessingBasisRepository.
type Repository struct {
	db    execer
	begin func(context.Context) (pgx.Tx, error)
}

// Readiness проверяет доступность соединения read-only без чтения или изменения данных.
func (r *Repository) Readiness(ctx context.Context) error {
	var one int
	return r.db.QueryRow(ctx, `SELECT 1`).Scan(&one)
}

// Repository реализует порт репозитория оснований обработки.
var _ ports.ProcessingBasisRepository = (*Repository)(nil)

// New создаёт репозиторий поверх пула соединений. Каждый Save выполняется в
// собственной транзакции.
func New(pool *pgxpool.Pool) *Repository {
	if pool == nil {
		panic("postgres: New с nil-пулом")
	}
	return &Repository{db: pool, begin: pool.Begin}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// expectedVersion — версия, прочитанная при загрузке; при создании основания она
// равна нулю. Создание вставляет строку, и занятый идентификатор отвергается
// ошибкой ports.ErrVersionConflict (ON CONFLICT DO NOTHING не трогает чужую
// версию). Изменение выполняется UPDATE ... WHERE version = $expected: ноль
// затронутых строк означает, что сохранённая версия не совпала с ожидаемой,
// поэтому и создание с уже занятым идентификатором, и устаревшее изменение дают
// один и тот же конфликт, а состояние не меняется.
func (r *Repository) Save(ctx context.Context, basis *domain.ProcessingBasis, expectedVersion int) error {
	if basis == nil {
		return errors.New("postgres: сохранение nil-основания")
	}
	snapshot := basis.Snapshot()
	return r.withTransaction(ctx, func(q execer) error {
		return saveSnapshot(ctx, q, snapshot, expectedVersion)
	})
}

// FindByID загружает основание по идентификатору через
// domain.RehydrateProcessingBasis. Отсутствие даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrBasisNotFound).
func (r *Repository) FindByID(ctx context.Context, id domain.BasisID) (*domain.ProcessingBasis, error) {
	snapshot, err := loadSnapshot(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	basis, err := domain.RehydrateProcessingBasis(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление основания %s: %w", id, err)
	}
	return basis, nil
}

// FindByTenant возвращает основания тенанта, упорядоченные по идентификатору,
// чтобы выдача была воспроизводимой.
func (r *Repository) FindByTenant(ctx context.Context, tenant domain.TenantID) ([]*domain.ProcessingBasis, error) {
	return r.list(ctx, selectBasisIDsByTenant, tenant.String())
}

// ListExpired возвращает действующие основания, окно действия которых истекло к
// моменту at. Обслуживает фоновое истечение полномочий роли worker.
func (r *Repository) ListExpired(ctx context.Context, at time.Time) ([]*domain.ProcessingBasis, error) {
	return r.list(ctx, selectExpiredBasisIDs, string(domain.BasisActive), at)
}

// list читает идентификаторы запросом и восстанавливает каждое основание через
// FindByID, поэтому результат упорядочен по идентификатору.
func (r *Repository) list(ctx context.Context, query string, args ...any) ([]*domain.ProcessingBasis, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение идентификаторов оснований: %w", err)
	}
	defer rows.Close()

	ids := make([]domain.BasisID, 0)
	for rows.Next() {
		var id domain.BasisID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: разбор идентификатора основания: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение идентификаторов оснований: %w", err)
	}

	bases := make([]*domain.ProcessingBasis, 0, len(ids))
	for _, id := range ids {
		basis, err := r.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		bases = append(bases, basis)
	}
	return bases, nil
}

// withTransaction выполняет fn в транзакции, если репозиторий привязан к пулу.
// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
// ошибка отката намеренно игнорируется.
func (r *Repository) withTransaction(ctx context.Context, fn func(execer) error) error {
	if r.begin == nil {
		return fn(r.db)
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: открытие транзакции: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: коммит транзакции: %w", err)
	}
	return nil
}

// saveSnapshot записывает снимок агрегата в рамках переданного исполнителя.
// Подчинённая таблица стоп-листа предварительно очищается: снимок содержит
// агрегат целиком, поэтому полная замена не оставляет строк, удалённых из
// агрегата.
func saveSnapshot(ctx context.Context, q execer, snapshot domain.ProcessingBasisSnapshot, expectedVersion int) error {
	var (
		tag pgconn.CommandTag
		err error
	)
	if expectedVersion == 0 {
		tag, err = q.Exec(ctx, insertBasis,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			snapshot.PurposeName,
			string(snapshot.Category),
			snapshot.Retention,
			snapshot.ValidFrom,
			snapshot.ValidUntil,
			string(snapshot.Status),
			snapshot.Version,
		)
	} else {
		tag, err = q.Exec(ctx, updateBasis,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			snapshot.PurposeName,
			string(snapshot.Category),
			snapshot.Retention,
			snapshot.ValidFrom,
			snapshot.ValidUntil,
			string(snapshot.Status),
			snapshot.Version,
			expectedVersion,
		)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись основания %s: %w", snapshot.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: основание %s: ожидалась версия %d", ports.ErrVersionConflict, snapshot.ID, expectedVersion)
	}

	if _, err := q.Exec(ctx, deleteEntries, snapshot.ID.String()); err != nil {
		return fmt.Errorf("postgres: очистка стоп-листа основания %s: %w", snapshot.ID, err)
	}
	for position, entry := range snapshot.Entries {
		var revokedAt any
		if !entry.RevokedAt.IsZero() {
			revokedAt = entry.RevokedAt
		}
		if _, err := q.Exec(ctx, insertEntry,
			snapshot.ID.String(),
			position,
			entry.ID.String(),
			entry.Contact.String(),
			entry.Reason,
			entry.Active,
			entry.CreatedAt,
			revokedAt,
		); err != nil {
			return fmt.Errorf("postgres: запись стоп-листа %s основания %s: %w", entry.ID, snapshot.ID, err)
		}
	}
	return nil
}

// loadSnapshot читает снимок агрегата. Отсутствие основания даёт
// ports.ErrBasisNotFound.
func loadSnapshot(ctx context.Context, q execer, id domain.BasisID) (domain.ProcessingBasisSnapshot, error) {
	snapshot := domain.ProcessingBasisSnapshot{}
	var (
		category string
		status   string
	)
	err := q.QueryRow(ctx, selectBasis, id.String()).Scan(
		&snapshot.ID,
		&snapshot.TenantID,
		&snapshot.PurposeName,
		&category,
		&snapshot.Retention,
		&snapshot.ValidFrom,
		&snapshot.ValidUntil,
		&status,
		&snapshot.Version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProcessingBasisSnapshot{}, fmt.Errorf("%w: %s", ports.ErrBasisNotFound, id)
	}
	if err != nil {
		return domain.ProcessingBasisSnapshot{}, fmt.Errorf("postgres: чтение основания %s: %w", id, err)
	}
	snapshot.Category = domain.DataCategory(category)
	snapshot.Status = domain.BasisStatus(status)

	entries, err := queryEntries(ctx, q, id)
	if err != nil {
		return domain.ProcessingBasisSnapshot{}, err
	}
	snapshot.Entries = entries
	return snapshot, nil
}

// queryEntries читает записи стоп-листа в порядке внесения (колонка position).
func queryEntries(ctx context.Context, q execer, id domain.BasisID) ([]domain.SuppressionEntrySnapshot, error) {
	rows, err := q.Query(ctx, selectEntries, id.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение стоп-листа основания %s: %w", id, err)
	}
	defer rows.Close()

	entries := make([]domain.SuppressionEntrySnapshot, 0)
	for rows.Next() {
		entry := domain.SuppressionEntrySnapshot{}
		var revokedAt *time.Time
		if err := rows.Scan(
			&entry.ID,
			&entry.Contact,
			&entry.Reason,
			&entry.Active,
			&entry.CreatedAt,
			&revokedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: разбор записи стоп-листа основания %s: %w", id, err)
		}
		if revokedAt != nil {
			entry.RevokedAt = *revokedAt
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение стоп-листа основания %s: %w", id, err)
	}
	return entries, nil
}

// SQL-запросы адаптера. Схема указана литералом: адаптер работает только в схеме
// сервиса (Schema) и не параметризует её извне.
const (
	insertBasis = `
INSERT INTO "data-governance".processing_basis
    (id, tenant_id, purpose_name, category, retention_days, valid_from, valid_until, status, version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO NOTHING`

	updateBasis = `
UPDATE "data-governance".processing_basis
SET tenant_id      = $2,
    purpose_name   = $3,
    category       = $4,
    retention_days = $5,
    valid_from     = $6,
    valid_until    = $7,
    status         = $8,
    version        = $9
WHERE id = $1 AND version = $10`

	selectBasis = `
SELECT id, tenant_id, purpose_name, category, retention_days, valid_from, valid_until, status, version
FROM "data-governance".processing_basis
WHERE id = $1`

	selectBasisIDsByTenant = `
SELECT id
FROM "data-governance".processing_basis
WHERE tenant_id = $1
ORDER BY id`

	selectExpiredBasisIDs = `
SELECT id
FROM "data-governance".processing_basis
WHERE status = $1 AND valid_until <= $2
ORDER BY id`

	deleteEntries = `DELETE FROM "data-governance".suppression_entry WHERE basis_id = $1`

	insertEntry = `
INSERT INTO "data-governance".suppression_entry
    (basis_id, position, id, contact, reason, active, created_at, revoked_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	selectEntries = `
SELECT id, contact, reason, active, created_at, revoked_at
FROM "data-governance".suppression_entry
WHERE basis_id = $1
ORDER BY position`
)
