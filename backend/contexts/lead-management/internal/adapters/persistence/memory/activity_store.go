package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// LeadActivityStore — in-memory реализация ports.LeadActivityStore.
type LeadActivityStore struct {
	mu         sync.RWMutex
	activities map[string]ports.LeadActivity
}

// LeadActivityStore реализует порт хранилища истории переходов.
var _ ports.LeadActivityStore = (*LeadActivityStore)(nil)

// NewLeadActivityStore создаёт пустое хранилище.
func NewLeadActivityStore() *LeadActivityStore {
	return &LeadActivityStore{activities: make(map[string]ports.LeadActivity)}
}

// RecordStageChange фиксирует переход лида на стадию. Идентификатор записи
// генерируется адаптером.
func (s *LeadActivityStore) RecordStageChange(_ context.Context, leadID domain.CompanyID, stage domain.Stage, author string, at time.Time) error {
	id, err := newActivityID()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activities[id] = ports.LeadActivity{
		ID:     id,
		LeadID: leadID,
		Stage:  stage,
		Author: author,
		At:     at,
	}
	return nil
}

// ListByLead возвращает историю переходов лида, старые первыми.
func (s *LeadActivityStore) ListByLead(_ context.Context, leadID domain.CompanyID) ([]ports.LeadActivity, error) {
	s.mu.RLock()
	activities := make([]ports.LeadActivity, 0)
	for _, activity := range s.activities {
		if activity.LeadID != leadID {
			continue
		}
		activities = append(activities, activity)
	}
	s.mu.RUnlock()

	sort.Slice(activities, func(i, j int) bool {
		if activities[i].At.Equal(activities[j].At) {
			return activities[i].ID < activities[j].ID
		}
		return activities[i].At.Before(activities[j].At)
	})
	return activities, nil
}

// newActivityID генерирует случайный идентификатор записи (32 hex-символа).
func newActivityID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("memory: генерация идентификатора: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
