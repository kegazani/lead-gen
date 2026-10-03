package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// DraftStore — PostgreSQL-реализация ports.EmailDraftStore.
//
// Черновик — производный материал оператора, а не часть агрегата заявки,
// поэтому хранится отдельной таблицей. Порядок найденных лидов задаёт колонка
// position: повторный черновик по тому же лиду заменяет прежний, сохраняя место
// в порядке, — шаг идемпотентен и переживает повторный запуск activity. Чтение
// изолировано тенантом: чужие черновики не возвращаются.
type DraftStore struct {
	db execer
}

// DraftStore реализует порт хранилища черновиков писем.
var _ ports.EmailDraftStore = (*DraftStore)(nil)

// NewDraftStore создаёт хранилище черновиков поверх пула соединений.
func NewDraftStore(pool *pgxpool.Pool) *DraftStore {
	if pool == nil {
		panic("postgres: NewDraftStore с nil-пулом")
	}
	return &DraftStore{db: pool}
}

// SaveDrafts сохраняет черновики, добавляя их к уже имеющимся. Черновик с
// существующим лидом заявки заменяется на месте: position при этом не меняется.
func (s *DraftStore) SaveDrafts(ctx context.Context, drafts []ports.EmailDraft) error {
	for _, draft := range drafts {
		if _, err := s.db.Exec(ctx, upsertEmailDraft,
			draft.RequestID,
			draft.TenantID,
			draft.LeadID,
			draft.Subject,
			draft.Body,
			draft.Language,
		); err != nil {
			return fmt.Errorf("postgres: сохранение черновика %s/%s: %w", draft.RequestID, draft.LeadID, err)
		}
	}
	return nil
}

// ListByRequest возвращает черновики заявки, принадлежащие тенанту, в порядке
// найденных лидов.
func (s *DraftStore) ListByRequest(ctx context.Context, requestID, tenantID string) ([]ports.EmailDraft, error) {
	rows, err := s.db.Query(ctx, selectEmailDrafts, requestID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение черновиков заявки %s: %w", requestID, err)
	}
	defer rows.Close()

	drafts := make([]ports.EmailDraft, 0)
	for rows.Next() {
		draft := ports.EmailDraft{RequestID: requestID, TenantID: tenantID}
		if err := rows.Scan(&draft.LeadID, &draft.Subject, &draft.Body, &draft.Language); err != nil {
			return nil, fmt.Errorf("postgres: разбор черновика заявки %s: %w", requestID, err)
		}
		drafts = append(drafts, draft)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение черновиков заявки %s: %w", requestID, err)
	}
	return drafts, nil
}

// SQL-запросы хранилища черновиков.
const (
	// Позиция нового черновика — следующий номер заявки; при конфликте по лиду
	// position не обновляется, поэтому прежнее место сохраняется.
	upsertEmailDraft = `
INSERT INTO "request-fulfillment".email_draft (request_id, tenant_id, lead_id, subject, body, language, position)
VALUES (
    $1, $2, $3, $4, $5, $6,
    COALESCE((SELECT MAX(position) + 1 FROM "request-fulfillment".email_draft WHERE request_id = $1), 0)
)
ON CONFLICT (request_id, lead_id) DO UPDATE SET
    tenant_id = EXCLUDED.tenant_id,
    subject   = EXCLUDED.subject,
    body      = EXCLUDED.body,
    language  = EXCLUDED.language`

	selectEmailDrafts = `
SELECT lead_id, subject, body, language
FROM "request-fulfillment".email_draft
WHERE request_id = $1 AND tenant_id = $2
ORDER BY position ASC, lead_id ASC`
)
