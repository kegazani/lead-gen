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

// EmailTemplateRepository — PostgreSQL-реализация ports.EmailTemplateRepository.
type EmailTemplateRepository struct {
	db execer
}

// EmailTemplateRepository реализует порт репозитория шаблонов письма.
var _ ports.EmailTemplateRepository = (*EmailTemplateRepository)(nil)

// NewEmailTemplateRepository создаёт репозиторий поверх пула соединений.
func NewEmailTemplateRepository(pool *pgxpool.Pool) *EmailTemplateRepository {
	if pool == nil {
		panic("postgres: NewEmailTemplateRepository с nil-пулом")
	}
	return &EmailTemplateRepository{db: pool}
}

// Save сохраняет шаблон целиком как снимок в JSONB (upsert по идентификатору).
func (r *EmailTemplateRepository) Save(ctx context.Context, template *domain.EmailTemplate) error {
	if template == nil {
		return errors.New("postgres: сохранение nil-шаблона")
	}
	snapshot := template.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация шаблона %s: %w", snapshot.ID, err)
	}
	if _, err := r.db.Exec(ctx, upsertEmailTemplate,
		snapshot.ID.String(),
		snapshot.Name,
		snapshot.Revision,
		string(raw),
		snapshot.CreatedAt,
		snapshot.UpdatedAt,
	); err != nil {
		return fmt.Errorf("postgres: запись шаблона %s: %w", snapshot.ID, err)
	}
	return nil
}

// FindByID загружает шаблон по идентификатору через
// domain.RehydrateEmailTemplate. Отсутствие даёт ports.ErrTemplateNotFound.
func (r *EmailTemplateRepository) FindByID(ctx context.Context, id domain.TemplateID) (*domain.EmailTemplate, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectEmailTemplate, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ports.ErrTemplateNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение шаблона %s: %w", id, err)
	}
	template, err := unmarshalEmailTemplateSnapshot([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("postgres: разбор снимка шаблона %s: %w", id, err)
	}
	return domain.RehydrateEmailTemplate(template)
}

// Delete удаляет шаблон по идентификатору. Отсутствие даёт
// ports.ErrTemplateNotFound.
func (r *EmailTemplateRepository) Delete(ctx context.Context, id domain.TemplateID) error {
	tag, err := r.db.Exec(ctx, deleteEmailTemplate, id.String())
	if err != nil {
		return fmt.Errorf("postgres: удаление шаблона %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ports.ErrTemplateNotFound, id)
	}
	return nil
}

// Search ищет шаблоны по нормализованному запросу (имя, тема, тело, объявленные
// переменные). Фильтрация и порядок выполняются на сервере общей с in-memory
// семантикой ports.TemplateMatches / ports.SortTemplates, поэтому реализация не
// расходится с памятью.
func (r *EmailTemplateRepository) Search(ctx context.Context, query string, limit int) ([]*domain.EmailTemplate, error) {
	normalized := ports.NormalizeTemplateQuery(query)
	rows, err := r.db.Query(ctx, selectEmailTemplates)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение шаблонов: %w", err)
	}
	defer rows.Close()

	templates := make([]*domain.EmailTemplate, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор шаблона: %w", err)
		}
		snapshot, err := unmarshalEmailTemplateSnapshot([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка шаблона: %w", err)
		}
		template, err := domain.RehydrateEmailTemplate(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление шаблона %s: %w", snapshot.ID, err)
		}
		if !ports.TemplateMatches(template, normalized) {
			continue
		}
		templates = append(templates, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение шаблонов: %w", err)
	}
	ports.SortTemplates(templates)
	return ports.ApplyTemplateLimit(templates, limit), nil
}

// unmarshalEmailTemplateSnapshot разбирает JSONB-снимок шаблона.
func unmarshalEmailTemplateSnapshot(raw []byte) (domain.EmailTemplateSnapshot, error) {
	var snapshot domain.EmailTemplateSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return domain.EmailTemplateSnapshot{}, err
	}
	return snapshot, nil
}

// SQL-запросы адаптера шаблонов письма.
const (
	upsertEmailTemplate = `
INSERT INTO "lead-management".email_template (id, name, revision, snapshot, created_at, updated_at)
VALUES ($1, $2, $3, $4::jsonb, $5, $6)
ON CONFLICT (id) DO UPDATE SET
    name       = EXCLUDED.name,
    revision   = EXCLUDED.revision,
    snapshot   = EXCLUDED.snapshot,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at`

	selectEmailTemplate = `
SELECT snapshot::text
FROM "lead-management".email_template
WHERE id = $1`

	selectEmailTemplates = `
SELECT snapshot::text
FROM "lead-management".email_template
ORDER BY name ASC, id ASC`

	deleteEmailTemplate = `
DELETE FROM "lead-management".email_template
WHERE id = $1`
)
