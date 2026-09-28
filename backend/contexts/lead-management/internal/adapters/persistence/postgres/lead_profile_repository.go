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
)

// LeadProfileRepository — PostgreSQL-реализация ports.LeadProfileRepository.
type LeadProfileRepository struct {
	db execer
}

// LeadProfileRepository реализует порт репозитория карточек.
var _ ports.LeadProfileRepository = (*LeadProfileRepository)(nil)

// NewLeadProfileRepository создаёт репозиторий поверх пула соединений.
func NewLeadProfileRepository(pool *pgxpool.Pool) *LeadProfileRepository {
	if pool == nil {
		panic("postgres: NewLeadProfileRepository с nil-пулом")
	}
	return &LeadProfileRepository{db: pool}
}

// Save сохраняет карточку целиком как снимок в JSONB. Повторный вызов для той же
// компании заменяет прежний снимок (upsert по идентификатору).
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
		snapshot.Hidden,
		string(raw),
		snapshot.CreatedAt,
		snapshot.UpdatedAt,
	); err != nil {
		return fmt.Errorf("postgres: запись карточки %s: %w", snapshot.ID, err)
	}
	return nil
}

// FindByID загружает карточку по идентификатору через domain.RehydrateCompany.
// Отсутствие карточки даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrProfileNotFound).
func (r *LeadProfileRepository) FindByID(ctx context.Context, id domain.CompanyID) (*domain.Company, error) {
	snapshot, err := r.loadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	company, err := domain.RehydrateCompany(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление карточки %s: %w", id, err)
	}
	return company, nil
}

// ListVisible возвращает не скрытые карточки, новые первыми, не более limit
// штук. Фильтрация по флагу hidden выполняется в запросе, поэтому скрытые
// карточки не читаются и не рехидратируются.
func (r *LeadProfileRepository) ListVisible(ctx context.Context, limit int) ([]*domain.Company, error) {
	if limit <= 0 {
		return []*domain.Company{}, nil
	}
	rows, err := r.db.Query(ctx, selectVisibleLeadProfiles, limit)
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

// ListSnoozed возвращает отложенных лидов (snooze_until != nil), новые первыми.
// Фильтрация по snooze выполняется в Go после чтения снимков: поле лежит в
// JSONB-снимке, а не в отдельной колонке.
func (r *LeadProfileRepository) ListSnoozed(ctx context.Context) ([]*domain.Company, error) {
	rows, err := r.db.Query(ctx, selectAllVisibleLeadProfiles)
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

// Delete удаляет карточку безвозвратно. Удаление отсутствующей карточки — не
// ошибка (операция идемпотентна).
func (r *LeadProfileRepository) Delete(ctx context.Context, id domain.CompanyID) error {
	if _, err := r.db.Exec(ctx, deleteLeadProfile, id.String()); err != nil {
		return fmt.Errorf("postgres: удаление карточки %s: %w", id, err)
	}
	return nil
}

// loadSnapshot читает снимок карточки. Отсутствие карточки даёт
// ports.ErrProfileNotFound.
func (r *LeadProfileRepository) loadSnapshot(ctx context.Context, id domain.CompanyID) (domain.CompanySnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectLeadProfile, id.String()).Scan(&raw)
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
// только в схеме сервиса (Schema) и не параметризует её извне.
const (
	upsertLeadProfile = `
INSERT INTO "lead-management".lead_profile (id, hidden, snapshot, created_at, updated_at)
VALUES ($1, $2, $3::jsonb, $4, $5)
ON CONFLICT (id) DO UPDATE SET
    hidden     = EXCLUDED.hidden,
    snapshot   = EXCLUDED.snapshot,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at`

	selectLeadProfile = `
SELECT snapshot::text
FROM "lead-management".lead_profile
WHERE id = $1`

	selectVisibleLeadProfiles = `
SELECT snapshot::text
FROM "lead-management".lead_profile
WHERE hidden = FALSE
ORDER BY created_at DESC, id DESC
LIMIT $1`

	selectAllVisibleLeadProfiles = `
SELECT snapshot::text
FROM "lead-management".lead_profile
WHERE hidden = FALSE
ORDER BY created_at DESC, id DESC`

	deleteLeadProfile = `
DELETE FROM "lead-management".lead_profile
WHERE id = $1`
)
