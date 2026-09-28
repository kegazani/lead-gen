package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// LeadActivityStore — PostgreSQL-реализация ports.LeadActivityStore.
type LeadActivityStore struct {
	db execer
}

// LeadActivityStore реализует порт хранилища истории переходов.
var _ ports.LeadActivityStore = (*LeadActivityStore)(nil)

// NewLeadActivityStore создаёт хранилище поверх пула соединений.
func NewLeadActivityStore(pool *pgxpool.Pool) *LeadActivityStore {
	if pool == nil {
		panic("postgres: NewLeadActivityStore с nil-пулом")
	}
	return &LeadActivityStore{db: pool}
}

// RecordStageChange фиксирует переход лида на стадию. Идентификатор записи
// генерируется адаптером.
func (s *LeadActivityStore) RecordStageChange(ctx context.Context, leadID domain.CompanyID, stage domain.Stage, author string, at time.Time) error {
	id, err := newActivityID()
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, insertLeadActivity,
		id,
		leadID.String(),
		string(stage),
		author,
		at,
	); err != nil {
		return fmt.Errorf("postgres: запись перехода лида %s: %w", leadID, err)
	}
	return nil
}

// ListByLead возвращает историю переходов лида, старые первыми.
func (s *LeadActivityStore) ListByLead(ctx context.Context, leadID domain.CompanyID) ([]ports.LeadActivity, error) {
	rows, err := s.db.Query(ctx, selectLeadActivities, leadID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение истории лида %s: %w", leadID, err)
	}
	defer rows.Close()

	activities := make([]ports.LeadActivity, 0)
	for rows.Next() {
		var activity ports.LeadActivity
		if err := rows.Scan(&activity.ID, &activity.LeadID, &activity.Stage, &activity.Author, &activity.At); err != nil {
			return nil, fmt.Errorf("postgres: разбор перехода лида %s: %w", leadID, err)
		}
		activities = append(activities, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение истории лида %s: %w", leadID, err)
	}
	return activities, nil
}

// newActivityID генерирует случайный идентификатор записи (32 hex-символа).
func newActivityID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("postgres: генерация идентификатора: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// SQL-запросы адаптера истории переходов.
const (
	insertLeadActivity = `
INSERT INTO "lead-management".lead_activity (id, lead_id, stage, author, at)
VALUES ($1, $2, $3, $4, $5)`

	selectLeadActivities = `
SELECT id, lead_id, stage, author, at
FROM "lead-management".lead_activity
WHERE lead_id = $1
ORDER BY at ASC, id ASC`
)
