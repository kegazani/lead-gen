package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// PipelineRepository — PostgreSQL-реализация ports.PipelineRepository.
type PipelineRepository struct {
	db execer
}

// Реализация порта репозитория пайплайнов.
var _ ports.PipelineRepository = (*PipelineRepository)(nil)

// NewPipelineRepository создаёт репозиторий поверх пула соединений.
func NewPipelineRepository(pool *pgxpool.Pool) *PipelineRepository {
	if pool == nil {
		panic("postgres: NewPipelineRepository с nil-пулом")
	}
	return &PipelineRepository{db: pool}
}

// NewTxPipelineRepository создаёт репозиторий, привязанный к активной транзакции.
func NewTxPipelineRepository(tx pgx.Tx) *PipelineRepository {
	if tx == nil {
		panic("postgres: NewTxPipelineRepository с nil-транзакцией")
	}
	return &PipelineRepository{db: tx}
}

// Save сохраняет версию пайплайна с оптимистичной блокировкой по ревизии.
//
// expectedRevision == 0 — создание версии либо обновление строки с ревизией 0:
// условный upsert (ON CONFLICT ... DO UPDATE ... WHERE revision = 0), поэтому чужая
// ревизия не перетирается, а агрегат, прочитанный с ревизией 0 и изменённый,
// сохраняется. Иначе UPDATE ... WHERE revision = $expected: ноль затронутых строк
// означает конфликт ревизий.
func (r *PipelineRepository) Save(ctx context.Context, pipeline *domain.AgentPipeline, expectedRevision int) error {
	if pipeline == nil {
		return errors.New("postgres: сохранение nil-пайплайна")
	}
	snapshot := pipeline.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация пайплайна %s v%d: %w", snapshot.ID, snapshot.Version, err)
	}

	var tag pgconn.CommandTag
	if expectedRevision == 0 {
		tag, err = r.db.Exec(ctx, insertPipelineNew,
			string(snapshot.ID), string(snapshot.TenantID), snapshot.Version,
			snapshot.Published, snapshot.Revision, string(raw))
	} else {
		tag, err = r.db.Exec(ctx, updatePipeline,
			string(snapshot.ID), string(snapshot.TenantID), snapshot.Version,
			snapshot.Published, snapshot.Revision, string(raw), expectedRevision)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись пайплайна %s v%d: %w", snapshot.ID, snapshot.Version, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: пайплайн %s v%d: ожидалась ревизия %d", ports.ErrVersionConflict, snapshot.ID, snapshot.Version, expectedRevision)
	}
	return nil
}

// FindByID загружает версию пайплайна тенанта.
func (r *PipelineRepository) FindByID(ctx context.Context, id domain.PipelineID, version int, tenant domain.TenantID) (*domain.AgentPipeline, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectPipeline, string(tenant), string(id), version).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s v%d", ports.ErrPipelineNotFound, id, version)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение пайплайна %s v%d: %w", id, version, err)
	}
	var snapshot domain.PipelineSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, fmt.Errorf("postgres: разбор снимка пайплайна %s v%d: %w", id, version, err)
	}
	pipeline, err := domain.RehydratePipeline(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление пайплайна %s v%d: %w", id, version, err)
	}
	return pipeline, nil
}

// LatestPublished возвращает номер последней опубликованной версии пайплайна.
func (r *PipelineRepository) LatestPublished(ctx context.Context, id domain.PipelineID, tenant domain.TenantID) (int, error) {
	var latest *int
	err := r.db.QueryRow(ctx, selectLatestPublishedPipeline, string(tenant), string(id)).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) || latest == nil {
		return 0, fmt.Errorf("%w: %s", ports.ErrPipelineNotFound, id)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: чтение последней версии пайплайна %s: %w", id, err)
	}
	return *latest, nil
}

// RunRepository — PostgreSQL-реализация ports.RunRepository.
type RunRepository struct {
	db execer
}

// Реализация порта репозитория запусков.
var _ ports.RunRepository = (*RunRepository)(nil)

// NewRunRepository создаёт репозиторий поверх пула соединений.
func NewRunRepository(pool *pgxpool.Pool) *RunRepository {
	if pool == nil {
		panic("postgres: NewRunRepository с nil-пулом")
	}
	return &RunRepository{db: pool}
}

// NewTxRunRepository создаёт репозиторий, привязанный к активной транзакции.
func NewTxRunRepository(tx pgx.Tx) *RunRepository {
	if tx == nil {
		panic("postgres: NewTxRunRepository с nil-транзакцией")
	}
	return &RunRepository{db: tx}
}

// Save сохраняет запуск с оптимистичной блокировкой по ревизии.
//
// expectedRevision == 0 — создание запуска либо обновление строки с ревизией 0:
// условный upsert (ON CONFLICT ... DO UPDATE ... WHERE revision = 0). Иначе UPDATE ...
// WHERE revision = $expected: ноль затронутых строк означает конфликт ревизий.
func (r *RunRepository) Save(ctx context.Context, run *domain.AgentRun, expectedRevision int) error {
	if run == nil {
		return errors.New("postgres: сохранение nil-запуска")
	}
	snapshot := run.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация запуска %s: %w", snapshot.ID, err)
	}

	var tag pgconn.CommandTag
	if expectedRevision == 0 {
		tag, err = r.db.Exec(ctx, insertRunNew,
			string(snapshot.ID), string(snapshot.Mission.TenantID), string(snapshot.Status), snapshot.Revision, string(raw), snapshot.UpdatedAt)
	} else {
		tag, err = r.db.Exec(ctx, updateRun,
			string(snapshot.ID), string(snapshot.Mission.TenantID), string(snapshot.Status), snapshot.Revision, string(raw), snapshot.UpdatedAt, expectedRevision)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись запуска %s: %w", snapshot.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: запуск %s: ожидалась ревизия %d", ports.ErrVersionConflict, snapshot.ID, expectedRevision)
	}
	return nil
}

// FindByID загружает запуск тенанта.
func (r *RunRepository) FindByID(ctx context.Context, id domain.RunID, tenant domain.TenantID) (*domain.AgentRun, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectRun, string(id), string(tenant)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ports.ErrRunNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение запуска %s: %w", id, err)
	}
	return rehydrateRun(raw, id)
}

// ListByStatus возвращает запуски тенанта в заданном статусе, упорядоченные по
// идентификатору.
func (r *RunRepository) ListByStatus(ctx context.Context, status domain.RunStatus, tenant domain.TenantID, limit int) ([]*domain.AgentRun, error) {
	var lim any
	if limit > 0 {
		lim = limit
	}
	rows, err := r.db.Query(ctx, selectRunsByStatus, string(tenant), string(status), lim)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение запусков в статусе %q: %w", status, err)
	}
	defer rows.Close()

	runs := make([]*domain.AgentRun, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор запуска в статусе %q: %w", status, err)
		}
		run, err := rehydrateRun(raw, "")
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение запусков в статусе %q: %w", status, err)
	}
	return runs, nil
}

// List возвращает запуски тенанта по фильтру (лента/список консоли операций),
// упорядоченные по времени последнего изменения по убыванию, затем по
// идентификатору по убыванию. Пустой статус фильтра — все статусы. Выборка без
// тенанта отвергается (ErrMissingTenant).
func (r *RunRepository) List(ctx context.Context, filter ports.RunFilter) ([]*domain.AgentRun, error) {
	if filter.Tenant == "" {
		return nil, ports.ErrMissingTenant
	}
	var status any
	if filter.Status != "" {
		status = string(filter.Status)
	}
	var lim any
	if filter.Limit > 0 {
		lim = filter.Limit
	}
	rows, err := r.db.Query(ctx, selectRunFeed, string(filter.Tenant), status, lim)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение ленты запусков: %w", err)
	}
	defer rows.Close()

	runs := make([]*domain.AgentRun, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор ленты запусков: %w", err)
		}
		run, err := rehydrateRun(raw, "")
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение ленты запусков: %w", err)
	}
	return runs, nil
}

// CountActive возвращает число запусков тенанта, занимающих слот одновременной
// обработки.
func (r *RunRepository) CountActive(ctx context.Context, tenant domain.TenantID) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, countActiveRuns, string(tenant),
		string(domain.StatusRunning), string(domain.StatusAwaitingApproval), string(domain.StatusBlocked)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres: подсчёт активных запусков: %w", err)
	}
	return count, nil
}

// rehydrateRun восстанавливает запуск из снимка JSONB.
func rehydrateRun(raw string, id domain.RunID) (*domain.AgentRun, error) {
	var snapshot domain.RunSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, fmt.Errorf("postgres: разбор снимка запуска %s: %w", id, err)
	}
	run, err := domain.RehydrateRun(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление запуска %s: %w", snapshot.ID, err)
	}
	return run, nil
}

// SQL-запросы адаптеров пайплайнов и запусков.
//
// Вставка с expectedRevision == 0 — условный upsert: при отсутствии строки
// создаётся, при существующей строке с ревизией 0 обновляется, иначе (ревизия
// разошлась) — 0 затронутых строк и конфликт. Это позволяет сохранить агрегат,
// загруженный с ревизией 0 и изменённый, не выдавая его за новую запись.
const (
	insertPipelineNew = `
INSERT INTO "agent-orchestration".agent_pipeline (pipeline_id, tenant_id, version, published, revision, snapshot, updated_at)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, now())
ON CONFLICT (tenant_id, pipeline_id, version) DO UPDATE
SET published  = EXCLUDED.published,
    revision   = EXCLUDED.revision,
    snapshot   = EXCLUDED.snapshot,
    updated_at = now()
WHERE agent_pipeline.revision = 0`

	updatePipeline = `
UPDATE "agent-orchestration".agent_pipeline
SET published  = $4,
    revision   = $5,
    snapshot   = $6::jsonb,
    updated_at = now()
WHERE tenant_id = $2 AND pipeline_id = $1 AND version = $3 AND revision = $7`

	selectPipeline = `
SELECT snapshot::text
FROM "agent-orchestration".agent_pipeline
WHERE tenant_id = $1 AND pipeline_id = $2 AND version = $3`

	selectLatestPublishedPipeline = `
SELECT max(version)
FROM "agent-orchestration".agent_pipeline
WHERE tenant_id = $1 AND pipeline_id = $2 AND published`

	insertRunNew = `
INSERT INTO "agent-orchestration".agent_run (id, tenant_id, status, revision, snapshot, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)
ON CONFLICT (id) DO UPDATE
SET tenant_id  = EXCLUDED.tenant_id,
    status     = EXCLUDED.status,
    revision   = EXCLUDED.revision,
    snapshot   = EXCLUDED.snapshot,
    updated_at = EXCLUDED.updated_at
WHERE agent_run.revision = 0`

	updateRun = `
UPDATE "agent-orchestration".agent_run
SET tenant_id  = $2,
    status     = $3,
    revision   = $4,
    snapshot   = $5::jsonb,
    updated_at = $6
WHERE id = $1 AND revision = $7`

	selectRun = `
SELECT snapshot::text
FROM "agent-orchestration".agent_run
WHERE id = $1 AND tenant_id = $2`

	selectRunsByStatus = `
SELECT snapshot::text
FROM "agent-orchestration".agent_run
WHERE tenant_id = $1 AND status = $2
ORDER BY id
LIMIT $3`

	selectRunFeed = `
SELECT snapshot::text
FROM "agent-orchestration".agent_run
WHERE tenant_id = $1 AND ($2::text IS NULL OR status = $2)
ORDER BY updated_at DESC, id DESC
LIMIT $3`

	countActiveRuns = `
SELECT count(*)
FROM "agent-orchestration".agent_run
WHERE tenant_id = $1 AND status IN ($2, $3, $4)`
)
