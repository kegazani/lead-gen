package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/readiness"
)

// LeadProfileRepository — PostgreSQL-реализация ports.LeadProfileRepository.
type LeadProfileRepository struct {
	db   execer
	pool *pgxpool.Pool
}

// LeadProfileRepository реализует порт репозитория карточек.
var _ ports.LeadProfileRepository = (*LeadProfileRepository)(nil)

// NewLeadProfileRepository создаёт репозиторий поверх пула соединений.
func NewLeadProfileRepository(pool *pgxpool.Pool) *LeadProfileRepository {
	if pool == nil {
		panic("postgres: NewLeadProfileRepository с nil-пулом")
	}
	return &LeadProfileRepository{db: pool, pool: pool}
}

// ReadinessProbe returns a bounded, non-mutating check for the exact pool used
// by this repository. It performs no query against business data.
func (r *LeadProfileRepository) ReadinessProbe() readiness.Probe {
	return func(ctx context.Context) (readiness.ProbeResult, error) {
		if err := r.pool.Ping(ctx); err != nil {
			return readiness.ProbeResult{}, err
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
}

// Save сохраняет карточку целиком как снимок в JSONB в активном тенанте. Повторный
// вызов для той же компании заменяет прежний снимок (upsert по идентификатору).
func (r *LeadProfileRepository) Save(ctx context.Context, company *domain.Company) error {
	if company == nil {
		return errors.New("postgres: сохранение nil-карточки")
	}
	snapshot := company.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация карточки %s: %w", snapshot.ID, err)
	}
	if _, err := r.db.Exec(ctx, upsertLeadProfile,
		snapshot.ID.String(),
		ports.TenantFrom(ctx),
		snapshot.Hidden,
		string(raw),
		snapshot.CreatedAt,
		snapshot.UpdatedAt,
	); err != nil {
		return fmt.Errorf("postgres: запись карточки %s: %w", snapshot.ID, err)
	}
	return nil
}

// FindByID загружает карточку по идентификатору в активном тенанте через
// domain.RehydrateCompany. Карточка чужого тенанта недостижима: для неё ошибка
// удовлетворяет errors.Is(err, ports.ErrProfileNotFound).
func (r *LeadProfileRepository) FindByID(ctx context.Context, id domain.CompanyID) (*domain.Company, error) {
	snapshot, err := r.loadSnapshot(ctx, id, ports.TenantFrom(ctx))
	if err != nil {
		return nil, err
	}
	company, err := domain.RehydrateCompany(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление карточки %s: %w", id, err)
	}
	return company, nil
}

// ListVisible возвращает не скрытые карточки активного тенанта, новые первыми, не
// более limit штук. Фильтрация по тенанту и флагу hidden выполняется в запросе,
// поэтому чужие и скрытые карточки не читаются и не рехидратируются.
func (r *LeadProfileRepository) ListVisible(ctx context.Context, limit int) ([]*domain.Company, error) {
	if limit <= 0 {
		return []*domain.Company{}, nil
	}
	rows, err := r.db.Query(ctx, selectVisibleLeadProfiles, ports.TenantFrom(ctx), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение витрины карточек: %w", err)
	}
	defer rows.Close()

	companies := make([]*domain.Company, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор карточки витрины: %w", err)
		}
		snapshot, err := unmarshalCompanySnapshot([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка карточки витрины: %w", err)
		}
		company, err := domain.RehydrateCompany(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление карточки %s: %w", snapshot.ID, err)
		}
		companies = append(companies, company)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение витрины карточек: %w", err)
	}
	return companies, nil
}

// ListSnoozed возвращает отложенных лидов активного тенанта (snooze_until != nil),
// новые первыми. Фильтрация по тенанту выполняется в запросе, по snooze — в Go
// после чтения снимков: поле лежит в JSONB-снимке, а не в отдельной колонке.
func (r *LeadProfileRepository) ListSnoozed(ctx context.Context) ([]*domain.Company, error) {
	rows, err := r.db.Query(ctx, selectAllVisibleLeadProfiles, ports.TenantFrom(ctx))
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение отложенных карточек: %w", err)
	}
	defer rows.Close()

	companies := make([]*domain.Company, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор карточки витрины: %w", err)
		}
		snapshot, err := unmarshalCompanySnapshot([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка карточки витрины: %w", err)
		}
		if snapshot.Status.SnoozeUntil == nil {
			continue
		}
		company, err := domain.RehydrateCompany(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление карточки %s: %w", snapshot.ID, err)
		}
		companies = append(companies, company)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение отложенных карточек: %w", err)
	}
	return companies, nil
}

// Delete удаляет карточку безвозвратно в активном тенанте. Удаление отсутствующей
// карточки или карточки чужого тенанта — не ошибка (операция идемпотентна и не
// пересекает границу тенанта).
func (r *LeadProfileRepository) Delete(ctx context.Context, id domain.CompanyID) error {
	if _, err := r.db.Exec(ctx, deleteLeadProfile, id.String(), ports.TenantFrom(ctx)); err != nil {
		return fmt.Errorf("postgres: удаление карточки %s: %w", id, err)
	}
	return nil
}

// loadSnapshot читает снимок карточки в активном тенанте. Отсутствие карточки или
// принадлежность чужому тенанту даёт ports.ErrProfileNotFound.
func (r *LeadProfileRepository) loadSnapshot(ctx context.Context, id domain.CompanyID, tenant string) (domain.CompanySnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectLeadProfile, id.String(), tenant).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CompanySnapshot{}, fmt.Errorf("%w: %s", ports.ErrProfileNotFound, id)
	}
	if err != nil {
		return domain.CompanySnapshot{}, fmt.Errorf("postgres: чтение карточки %s: %w", id, err)
	}
	snapshot, err := unmarshalCompanySnapshot([]byte(raw))
	if err != nil {
		return domain.CompanySnapshot{}, fmt.Errorf("postgres: разбор снимка карточки %s: %w", id, err)
	}
	return snapshot, nil
}

// unmarshalCompanySnapshot разбирает JSONB-снимок карточки.
func unmarshalCompanySnapshot(raw []byte) (domain.CompanySnapshot, error) {
	var snapshot domain.CompanySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return domain.CompanySnapshot{}, err
	}
	return snapshot, nil
}

// SQL-запросы адаптера карточек. Схема указана литералом: адаптер работает
// только в схеме сервиса (Schema) и не параметризует её извне. Активный тенант —
// область хранения: он есть в каждой выборке и в записи.
const (
	upsertLeadProfile = `
INSERT INTO "lead-management".lead_profile (id, tenant_id, hidden, snapshot, created_at, updated_at)
VALUES ($1, $2, $3, $4::jsonb, $5, $6)
ON CONFLICT (id) DO UPDATE SET
    tenant_id  = EXCLUDED.tenant_id,
    hidden     = EXCLUDED.hidden,
    snapshot   = EXCLUDED.snapshot,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at`

	selectLeadProfile = `
SELECT snapshot::text
FROM "lead-management".lead_profile
WHERE id = $1 AND tenant_id = $2`

	selectVisibleLeadProfiles = `
SELECT snapshot::text
FROM "lead-management".lead_profile
WHERE hidden = FALSE AND tenant_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2`

	selectAllVisibleLeadProfiles = `
SELECT snapshot::text
FROM "lead-management".lead_profile
WHERE hidden = FALSE AND tenant_id = $1
ORDER BY created_at DESC, id DESC`

	deleteLeadProfile = `
DELETE FROM "lead-management".lead_profile
WHERE id = $1 AND tenant_id = $2`
)
