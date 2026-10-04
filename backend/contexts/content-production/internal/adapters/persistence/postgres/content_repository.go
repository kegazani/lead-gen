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

// ContentRepository — PostgreSQL-реализация ports.ContentPackageRepository.
//
// Репозиторий не знает о транзакциях выше себя: Save выполняется одним
// оператором, а атомарность записи агрегата и его событий обеспечивает
// unit-of-work, привязывающий репозиторий к транзакции через NewContentTx.
type ContentRepository struct {
	db execer
}

// ContentRepository реализует порт репозитория контент-пакетов.
var _ ports.ContentPackageRepository = (*ContentRepository)(nil)

// NewContentRepository создаёт репозиторий контент-пакетов поверх пула соединений.
func NewContentRepository(pool *pgxpool.Pool) *ContentRepository {
	if pool == nil {
		panic("postgres: NewContentRepository с nil-пулом")
	}
	return &ContentRepository{db: pool}
}

// NewContentTx создаёт репозиторий, привязанный к активной транзакции. Запись идёт
// в рамках этой транзакции: commit и rollback остаются за unit-of-work.
func NewContentTx(tx pgx.Tx) *ContentRepository {
	if tx == nil {
		panic("postgres: NewContentTx с nil-транзакцией")
	}
	return &ContentRepository{db: tx}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// expectedVersion — версия, прочитанная при загрузке; при создании пакета она
// равна нулю. Создание вставляет строку, и занятый идентификатор отвергается
// ошибкой ports.ErrVersionConflict (ON CONFLICT DO NOTHING не трогает чужую
// версию). Изменение выполняется UPDATE ... WHERE version = $expected: ноль
// затронутых строк означает, что сохранённая версия не совпала с ожидаемой,
// поэтому и создание с ненулевой ожидаемой версией, и устаревшее изменение дают
// один и тот же конфликт, а состояние не меняется.
func (r *ContentRepository) Save(ctx context.Context, pkg *domain.ContentPackage, expectedVersion int) error {
	if pkg == nil {
		return errors.New("postgres: сохранение nil-контент-пакета")
	}
	snapshot := pkg.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация пакета %s: %w", snapshot.ID, err)
	}

	var tag pgconn.CommandTag
	if expectedVersion == 0 {
		tag, err = r.db.Exec(ctx, insertPackageNew,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			snapshot.Version,
			string(raw),
		)
	} else {
		tag, err = r.db.Exec(ctx, updatePackage,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			snapshot.Version,
			string(raw),
			expectedVersion,
		)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись пакета %s: %w", snapshot.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: пакет %s: ожидалась версия %d", ports.ErrVersionConflict, snapshot.ID, expectedVersion)
	}
	return nil
}

// FindByID загружает пакет по идентификатору через domain.RehydrateContentPackage.
// Отсутствие даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrContentPackageNotFound).
func (r *ContentRepository) FindByID(ctx context.Context, id domain.ContentAssetID) (*domain.ContentPackage, error) {
	snapshot, err := r.loadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	pkg, err := domain.RehydrateContentPackage(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление пакета %s: %w", id, err)
	}
	return pkg, nil
}

// loadSnapshot читает снимок пакета. Отсутствие даёт
// ports.ErrContentPackageNotFound.
func (r *ContentRepository) loadSnapshot(ctx context.Context, id domain.ContentAssetID) (domain.ContentPackageSnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectPackage, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ContentPackageSnapshot{}, fmt.Errorf("%w: %s", ports.ErrContentPackageNotFound, id)
	}
	if err != nil {
		return domain.ContentPackageSnapshot{}, fmt.Errorf("postgres: чтение пакета %s: %w", id, err)
	}
	var snapshot domain.ContentPackageSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return domain.ContentPackageSnapshot{}, fmt.Errorf("postgres: разбор снимка пакета %s: %w", id, err)
	}
	return snapshot, nil
}

// SQL-запросы адаптера контент-пакетов.
const (
	insertPackageNew = `
INSERT INTO "content-production".content_package (id, tenant_id, version, snapshot, updated_at)
VALUES ($1, $2, $3, $4::jsonb, now())
ON CONFLICT (id) DO NOTHING`

	updatePackage = `
UPDATE "content-production".content_package
SET tenant_id  = $2,
    version    = $3,
    snapshot   = $4::jsonb,
    updated_at = now()
WHERE id = $1 AND version = $5`

	selectPackage = `
SELECT snapshot::text
FROM "content-production".content_package
WHERE id = $1`
)
