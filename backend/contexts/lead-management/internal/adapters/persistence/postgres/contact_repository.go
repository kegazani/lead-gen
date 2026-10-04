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

// ContactRepository — PostgreSQL-реализация ports.ContactRepository.
type ContactRepository struct {
	db execer
}

// ContactRepository реализует порт репозитория контактов.
var _ ports.ContactRepository = (*ContactRepository)(nil)

// NewContactRepository создаёт репозиторий поверх пула соединений.
func NewContactRepository(pool *pgxpool.Pool) *ContactRepository {
	if pool == nil {
		panic("postgres: NewContactRepository с nil-пулом")
	}
	return &ContactRepository{db: pool}
}

// Save сохраняет контакт целиком как снимок в JSONB. Повторный вызов для того же
// контакта заменяет прежний снимок (upsert по идентификатору).
func (r *ContactRepository) Save(ctx context.Context, contact *domain.Contact) error {
	if contact == nil {
		return errors.New("postgres: сохранение nil-контакта")
	}
	snapshot := contact.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация контакта %s: %w", snapshot.ID, err)
	}
	if _, err := r.db.Exec(ctx, upsertContact,
		snapshot.ID.String(),
		snapshot.CompanyID.String(),
		string(raw),
		snapshot.CreatedAt,
		snapshot.UpdatedAt,
	); err != nil {
		return fmt.Errorf("postgres: запись контакта %s: %w", snapshot.ID, err)
	}
	return nil
}

// FindByID загружает контакт по идентификатору через domain.RehydrateContact.
// Отсутствие контакта даёт нейтральную ошибку: у порта контактов нет отдельного
// sentinel отсутствия сущности.
func (r *ContactRepository) FindByID(ctx context.Context, id domain.ContactID) (*domain.Contact, error) {
	snapshot, err := r.loadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	contact, err := domain.RehydrateContact(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление контакта %s: %w", id, err)
	}
	return contact, nil
}

// ListByCompany возвращает контакты компании в порядке постановки
// (created_at, id).
func (r *ContactRepository) ListByCompany(ctx context.Context, companyID domain.CompanyID) ([]*domain.Contact, error) {
	rows, err := r.db.Query(ctx, selectContactsByCompany, companyID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение контактов компании %s: %w", companyID, err)
	}
	defer rows.Close()

	contacts := make([]*domain.Contact, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор контакта компании %s: %w", companyID, err)
		}
		snapshot, err := unmarshalContactSnapshot([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка контакта: %w", err)
		}
		contact, err := domain.RehydrateContact(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление контакта %s: %w", snapshot.ID, err)
		}
		contacts = append(contacts, contact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение контактов компании %s: %w", companyID, err)
	}
	return contacts, nil
}

// ListByCompanies возвращает контакты набора компаний одним запросом
// (WHERE company_id = ANY(...)) в порядке (company_id, created_at, id). Пустой
// набор даёт пустой список без обращения к базе.
func (r *ContactRepository) ListByCompanies(ctx context.Context, companyIDs []domain.CompanyID) ([]*domain.Contact, error) {
	if len(companyIDs) == 0 {
		return []*domain.Contact{}, nil
	}

	ids := make([]string, 0, len(companyIDs))
	for _, companyID := range companyIDs {
		ids = append(ids, companyID.String())
	}

	rows, err := r.db.Query(ctx, selectContactsByCompanies, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение контактов компаний: %w", err)
	}
	defer rows.Close()

	contacts := make([]*domain.Contact, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор контакта: %w", err)
		}
		snapshot, err := unmarshalContactSnapshot([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка контакта: %w", err)
		}
		contact, err := domain.RehydrateContact(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление контакта %s: %w", snapshot.ID, err)
		}
		contacts = append(contacts, contact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение контактов компаний: %w", err)
	}
	return contacts, nil
}

// loadSnapshot читает снимок контакта.
func (r *ContactRepository) loadSnapshot(ctx context.Context, id domain.ContactID) (domain.ContactSnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectContact, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ContactSnapshot{}, fmt.Errorf("%w: контакт %s", ports.ErrContactNotFound, id)
	}
	if err != nil {
		return domain.ContactSnapshot{}, fmt.Errorf("postgres: чтение контакта %s: %w", id, err)
	}
	snapshot, err := unmarshalContactSnapshot([]byte(raw))
	if err != nil {
		return domain.ContactSnapshot{}, fmt.Errorf("postgres: разбор снимка контакта %s: %w", id, err)
	}
	return snapshot, nil
}

// unmarshalContactSnapshot разбирает JSONB-снимок контакта.
func unmarshalContactSnapshot(raw []byte) (domain.ContactSnapshot, error) {
	var snapshot domain.ContactSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return domain.ContactSnapshot{}, err
	}
	return snapshot, nil
}

// SQL-запросы адаптера контактов.
const (
	upsertContact = `
INSERT INTO "lead-management".contact (id, company_id, snapshot, created_at, updated_at)
VALUES ($1, $2, $3::jsonb, $4, $5)
ON CONFLICT (id) DO UPDATE SET
    company_id = EXCLUDED.company_id,
    snapshot   = EXCLUDED.snapshot,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at`

	selectContact = `
SELECT snapshot::text
FROM "lead-management".contact
WHERE id = $1`

	selectContactsByCompany = `
SELECT snapshot::text
FROM "lead-management".contact
WHERE company_id = $1
ORDER BY created_at ASC, id ASC`

	selectContactsByCompanies = `
SELECT snapshot::text
FROM "lead-management".contact
WHERE company_id = ANY($1)
ORDER BY company_id ASC, created_at ASC, id ASC`
)
