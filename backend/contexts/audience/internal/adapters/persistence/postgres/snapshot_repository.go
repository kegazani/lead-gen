package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// SnapshotRepository — PostgreSQL-реализация ports.AudienceSnapshotRepository.
//
// Снимок аудитории неизменяем, поэтому оптимистичной блокировки нет: Save —
// upsert по идентификатору. Репозиторий не открывает транзакцию сам, если
// привязан к ней через newSnapshotTx: атомарность записи снимка и его события
// обеспечивает unit-of-work.
type SnapshotRepository struct {
	db execer
}

// SnapshotRepository реализует порт репозитория снимков аудитории.
var _ ports.AudienceSnapshotRepository = (*SnapshotRepository)(nil)

// NewSnapshotRepository создаёт репозиторий снимков поверх пула соединений.
func NewSnapshotRepository(pool *pgxpool.Pool) *SnapshotRepository {
	if pool == nil {
		panic("postgres: NewSnapshotRepository с nil-пулом")
	}
	return &SnapshotRepository{db: pool}
}

// newSnapshotTx создаёт репозиторий, привязанный к активной транзакции. Запись
// идёт в рамках этой транзакции: commit и rollback остаются за unit-of-work.
func newSnapshotTx(tx pgx.Tx) *SnapshotRepository {
	if tx == nil {
		panic("postgres: newSnapshotTx с nil-транзакцией")
	}
	return &SnapshotRepository{db: tx}
}

// Save сохраняет зафиксированный снимок (upsert по идентификатору). Повторное
// сохранение с тем же идентификатором заменяет запись, не создавая вторую.
func (r *SnapshotRepository) Save(ctx context.Context, snapshot *domain.AudienceSnapshot) error {
	if snapshot == nil {
		return errors.New("postgres: сохранение nil-снимка")
	}
	record := toAudienceSnapshotRecord(snapshot.State())
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("postgres: сериализация снимка %s: %w", record.ID, err)
	}
	if _, err := r.db.Exec(ctx, upsertAudienceSnapshot,
		record.ID,
		record.TenantID,
		record.Criteria.SegmentID,
		record.Version,
		string(raw),
	); err != nil {
		return fmt.Errorf("postgres: запись снимка %s: %w", record.ID, err)
	}
	return nil
}

// FindByID загружает снимок по идентификатору через
// domain.RehydrateAudienceSnapshot. Отсутствие даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrSnapshotNotFound).
func (r *SnapshotRepository) FindByID(ctx context.Context, id domain.SnapshotID) (*domain.AudienceSnapshot, error) {
	state, err := r.loadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot, err := domain.RehydrateAudienceSnapshot(state)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление снимка %s: %w", id, err)
	}
	return snapshot, nil
}

// ListBySegment возвращает снимки сегмента, упорядоченные по идентификатору.
func (r *SnapshotRepository) ListBySegment(ctx context.Context, segment domain.SegmentID) ([]*domain.AudienceSnapshot, error) {
	rows, err := r.db.Query(ctx, selectAudienceSnapshotsBySegment, segment.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение снимков сегмента %s: %w", segment, err)
	}
	defer rows.Close()

	snapshots := make([]*domain.AudienceSnapshot, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка сегмента %s: %w", segment, err)
		}
		state, err := decodeAudienceSnapshot(raw)
		if err != nil {
			return nil, err
		}
		snapshot, err := domain.RehydrateAudienceSnapshot(state)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление снимка %s: %w", state.ID, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение снимков сегмента %s: %w", segment, err)
	}
	return snapshots, nil
}

// loadSnapshot читает состояние снимка. Отсутствие даёт
// ports.ErrSnapshotNotFound.
func (r *SnapshotRepository) loadSnapshot(ctx context.Context, id domain.SnapshotID) (domain.AudienceSnapshotState, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectAudienceSnapshot, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AudienceSnapshotState{}, fmt.Errorf("%w: %s", ports.ErrSnapshotNotFound, id)
	}
	if err != nil {
		return domain.AudienceSnapshotState{}, fmt.Errorf("postgres: чтение снимка %s: %w", id, err)
	}
	return decodeAudienceSnapshot(raw)
}

// decodeAudienceSnapshot собирает состояние снимка из сохранённой JSONB-записи.
func decodeAudienceSnapshot(raw string) (domain.AudienceSnapshotState, error) {
	var record audienceSnapshotRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return domain.AudienceSnapshotState{}, fmt.Errorf("postgres: разбор снимка аудитории: %w", err)
	}
	return fromAudienceSnapshotRecord(record)
}

// SQL-запросы адаптера снимков аудитории.
const (
	upsertAudienceSnapshot = `
INSERT INTO "audience".audience_snapshot (id, tenant_id, segment_id, version, snapshot, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, now())
ON CONFLICT (id) DO UPDATE
SET tenant_id  = EXCLUDED.tenant_id,
    segment_id = EXCLUDED.segment_id,
    version    = EXCLUDED.version,
    snapshot   = EXCLUDED.snapshot,
    updated_at = now()`

	selectAudienceSnapshot = `
SELECT snapshot::text
FROM "audience".audience_snapshot
WHERE id = $1`

	selectAudienceSnapshotsBySegment = `
SELECT snapshot::text
FROM "audience".audience_snapshot
WHERE segment_id = $1
ORDER BY id`
)
