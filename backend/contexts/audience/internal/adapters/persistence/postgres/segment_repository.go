package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// SegmentRepository — PostgreSQL-реализация ports.SegmentRepository.
//
// Репозиторий не знает о транзакциях выше себя: Save выполняется одним
// оператором, а атомарность записи агрегата и его событий обеспечивает
// unit-of-work, привязывающий репозиторий к транзакции через newSegmentTx.
type SegmentRepository struct {
	db execer
}

// SegmentRepository реализует порт репозитория сегментов.
var _ ports.SegmentRepository = (*SegmentRepository)(nil)

// NewSegmentRepository создаёт репозиторий сегментов поверх пула соединений.
func NewSegmentRepository(pool *pgxpool.Pool) *SegmentRepository {
	if pool == nil {
		panic("postgres: NewSegmentRepository с nil-пулом")
	}
	return &SegmentRepository{db: pool}
}

// newSegmentTx создаёт репозиторий, привязанный к активной транзакции. Запись
// идёт в рамках этой транзакции: commit и rollback остаются за unit-of-work.
func newSegmentTx(tx pgx.Tx) *SegmentRepository {
	if tx == nil {
		panic("postgres: newSegmentTx с nil-транзакцией")
	}
	return &SegmentRepository{db: tx}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// expectedVersion — версия, прочитанная при загрузке; при создании сегмента она
// равна нулю. Создание вставляет строку, и занятый идентификатор отвергается
// ошибкой ports.ErrVersionConflict (ON CONFLICT DO NOTHING не трогает чужую
// версию). Изменение выполняется UPDATE ... WHERE version = $expected: ноль
// затронутых строк означает, что сохранённая версия не совпала с ожидаемой,
// поэтому и создание с ненулевой ожидаемой версией, и устаревшее изменение дают
// один и тот же конфликт, а состояние не меняется.
func (r *SegmentRepository) Save(ctx context.Context, segment *domain.Segment, expectedVersion int) error {
	if segment == nil {
		return errors.New("postgres: сохранение nil-сегмента")
	}
	record := toSegmentRecord(segment.Snapshot())
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("postgres: сериализация сегмента %s: %w", record.ID, err)
	}

	var tag pgconn.CommandTag
	if expectedVersion == 0 {
		tag, err = r.db.Exec(ctx, insertSegment,
			record.ID,
			record.TenantID,
			record.Version,
			string(raw),
		)
	} else {
		tag, err = r.db.Exec(ctx, updateSegment,
			record.ID,
			record.TenantID,
			record.Version,
			string(raw),
			expectedVersion,
		)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись сегмента %s: %w", record.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: сегмент %s: ожидалась версия %d", ports.ErrVersionConflict, record.ID, expectedVersion)
	}
	return nil
}

// FindByID загружает сегмент по идентификатору через domain.RehydrateSegment.
// Отсутствие даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrSegmentNotFound).
func (r *SegmentRepository) FindByID(ctx context.Context, id domain.SegmentID) (*domain.Segment, error) {
	snapshot, err := r.loadSegment(ctx, id)
	if err != nil {
		return nil, err
	}
	segment, err := domain.RehydrateSegment(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление сегмента %s: %w", id, err)
	}
	return segment, nil
}

// ListByTenant возвращает сегменты тенанта, упорядоченные по идентификатору.
func (r *SegmentRepository) ListByTenant(ctx context.Context, tenant domain.TenantID) ([]*domain.Segment, error) {
	return r.list(ctx, selectSegmentsByTenant, tenant.String())
}

// List возвращает все сегменты, упорядоченные по идентификатору. Обслуживает
// регулярный пересчёт роли schedule, охватывающий все тенанты.
func (r *SegmentRepository) List(ctx context.Context) ([]*domain.Segment, error) {
	return r.list(ctx, selectSegments)
}

// list читает сегменты запросом и восстанавливает каждый через
// domain.RehydrateSegment.
func (r *SegmentRepository) list(ctx context.Context, query string, args ...any) ([]*domain.Segment, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение сегментов: %w", err)
	}
	defer rows.Close()

	segments := make([]*domain.Segment, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор сегмента: %w", err)
		}
		snapshot, err := decodeSegment(raw)
		if err != nil {
			return nil, err
		}
		segment, err := domain.RehydrateSegment(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление сегмента %s: %w", snapshot.ID, err)
		}
		segments = append(segments, segment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение сегментов: %w", err)
	}
	return segments, nil
}

// loadSegment читает снимок сегмента. Отсутствие даёт ports.ErrSegmentNotFound.
func (r *SegmentRepository) loadSegment(ctx context.Context, id domain.SegmentID) (domain.SegmentSnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectSegment, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SegmentSnapshot{}, fmt.Errorf("%w: %s", ports.ErrSegmentNotFound, id)
	}
	if err != nil {
		return domain.SegmentSnapshot{}, fmt.Errorf("postgres: чтение сегмента %s: %w", id, err)
	}
	return decodeSegment(raw)
}

// decodeSegment собирает снимок сегмента из сохранённой JSONB-записи.
func decodeSegment(raw string) (domain.SegmentSnapshot, error) {
	var record segmentRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return domain.SegmentSnapshot{}, fmt.Errorf("postgres: разбор снимка сегмента: %w", err)
	}
	return fromSegmentRecord(record)
}

// SQL-запросы адаптера сегментов.
const (
	insertSegment = `
INSERT INTO "audience".segment (id, tenant_id, version, snapshot, updated_at)
VALUES ($1, $2, $3, $4::jsonb, now())
ON CONFLICT (id) DO NOTHING`

	updateSegment = `
UPDATE "audience".segment
SET tenant_id  = $2,
    version    = $3,
    snapshot   = $4::jsonb,
    updated_at = now()
WHERE id = $1 AND version = $5`

	selectSegment = `
SELECT snapshot::text
FROM "audience".segment
WHERE id = $1`

	selectSegmentsByTenant = `
SELECT snapshot::text
FROM "audience".segment
WHERE tenant_id = $1
ORDER BY id`

	selectSegments = `
SELECT snapshot::text
FROM "audience".segment
ORDER BY id`
)
