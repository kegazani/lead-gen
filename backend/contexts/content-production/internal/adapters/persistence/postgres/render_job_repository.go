package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// RenderJobRepository — PostgreSQL-реализация ports.RenderJobRepository.
//
// Денормализованная колонка state обслуживает выборку очереди рендеринга
// (ListPending), а version — оптимистичную блокировку.
type RenderJobRepository struct {
	db execer
}

// RenderJobRepository реализует порт репозитория заданий рендеринга.
var _ ports.RenderJobRepository = (*RenderJobRepository)(nil)

// NewRenderJobRepository создаёт репозиторий заданий поверх пула соединений.
func NewRenderJobRepository(pool *pgxpool.Pool) *RenderJobRepository {
	if pool == nil {
		panic("postgres: NewRenderJobRepository с nil-пулом")
	}
	return &RenderJobRepository{db: pool}
}

// NewRenderJobTx создаёт репозиторий, привязанный к активной транзакции. Запись
// идёт в рамках этой транзакции: commit и rollback остаются за unit-of-work.
func NewRenderJobTx(tx pgx.Tx) *RenderJobRepository {
	if tx == nil {
		panic("postgres: NewRenderJobTx с nil-транзакцией")
	}
	return &RenderJobRepository{db: tx}
}

// Save сохраняет задание целиком как снимок с оптимистичной блокировкой: создание
// с ожидаемой версией ноль вставляет строку (занятый идентификатор отвергается
// конфликтом), изменение идёт UPDATE ... WHERE version = $expected, и ноль
// затронутых строк означает конфликт версий.
func (r *RenderJobRepository) Save(ctx context.Context, job *domain.RenderJob, expectedVersion int) error {
	if job == nil {
		return errors.New("postgres: сохранение nil-задания рендеринга")
	}
	snapshot := job.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация задания %s: %w", snapshot.ID, err)
	}

	var tag pgconn.CommandTag
	if expectedVersion == 0 {
		tag, err = r.db.Exec(ctx, insertJobNew,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			string(snapshot.State),
			snapshot.Version,
			string(raw),
		)
	} else {
		tag, err = r.db.Exec(ctx, updateJob,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			string(snapshot.State),
			snapshot.Version,
			string(raw),
			expectedVersion,
		)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись задания %s: %w", snapshot.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: задание %s: ожидалась версия %d", ports.ErrVersionConflict, snapshot.ID, expectedVersion)
	}
	return nil
}

// FindByID загружает задание по идентификатору через domain.RehydrateRenderJob.
// Отсутствие даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrRenderJobNotFound).
func (r *RenderJobRepository) FindByID(ctx context.Context, id domain.RenderJobID) (*domain.RenderJob, error) {
	snapshot, err := r.loadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	job, err := domain.RehydrateRenderJob(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление задания %s: %w", id, err)
	}
	return job, nil
}

// ListPending возвращает задания, ожидающие исполнения (requested или rendering),
// в порядке идентификатора, чтобы выдача была воспроизводимой. limit больше нуля
// ограничивает размер порции; limit <= 0 возвращает все ожидающие.
func (r *RenderJobRepository) ListPending(ctx context.Context, limit int) ([]*domain.RenderJob, error) {
	var lim any
	if limit > 0 {
		lim = limit
	}
	rows, err := r.db.Query(ctx, selectPendingJobs, lim)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение очереди рендеринга: %w", err)
	}
	defer rows.Close()

	jobs := make([]*domain.RenderJob, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор задания рендеринга: %w", err)
		}
		var snapshot domain.RenderJobSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка задания: %w", err)
		}
		job, err := domain.RehydrateRenderJob(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление задания %s: %w", snapshot.ID, err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение очереди рендеринга: %w", err)
	}
	return jobs, nil
}

// loadSnapshot читает снимок задания. Отсутствие даёт ports.ErrRenderJobNotFound.
func (r *RenderJobRepository) loadSnapshot(ctx context.Context, id domain.RenderJobID) (domain.RenderJobSnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectJob, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RenderJobSnapshot{}, fmt.Errorf("%w: %s", ports.ErrRenderJobNotFound, id)
	}
	if err != nil {
		return domain.RenderJobSnapshot{}, fmt.Errorf("postgres: чтение задания %s: %w", id, err)
	}
	var snapshot domain.RenderJobSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return domain.RenderJobSnapshot{}, fmt.Errorf("postgres: разбор снимка задания %s: %w", id, err)
	}
	return snapshot, nil
}

// SQL-запросы адаптера заданий рендеринга.
const (
	insertJobNew = `
INSERT INTO "content-production".render_job (id, tenant_id, state, version, snapshot, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, now())
ON CONFLICT (id) DO NOTHING`

	updateJob = `
UPDATE "content-production".render_job
SET tenant_id  = $2,
    state      = $3,
    version    = $4,
    snapshot   = $5::jsonb,
    updated_at = now()
WHERE id = $1 AND version = $6`

	selectJob = `
SELECT snapshot::text
FROM "content-production".render_job
WHERE id = $1`

	selectPendingJobs = `
SELECT snapshot::text
FROM "content-production".render_job
WHERE state IN ('requested', 'rendering')
ORDER BY id
LIMIT $1`
)
