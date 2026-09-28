package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// LeadProfileRepository — in-memory реализация ports.LeadProfileRepository.
type LeadProfileRepository struct {
	mu        sync.RWMutex
	companies map[domain.CompanyID]domain.CompanySnapshot
}

// LeadProfileRepository реализует порт репозитория карточек.
var _ ports.LeadProfileRepository = (*LeadProfileRepository)(nil)

// NewLeadProfileRepository создаёт пустой репозиторий.
func NewLeadProfileRepository() *LeadProfileRepository {
	return &LeadProfileRepository{companies: make(map[domain.CompanyID]domain.CompanySnapshot)}
}

// Save сохраняет карточку целиком как снимок. Повторный вызов для той же
// компании заменяет прежний снимок (upsert по идентификатору).
func (r *LeadProfileRepository) Save(_ context.Context, company *domain.Company) error {
	if company == nil {
		return errors.New("memory: сохранение nil-карточки")
	}
	snapshot := company.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.companies[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает карточку по идентификатору через domain.RehydrateCompany.
// Отсутствие карточки даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrProfileNotFound).
func (r *LeadProfileRepository) FindByID(_ context.Context, id domain.CompanyID) (*domain.Company, error) {
	r.mu.RLock()
	snapshot, ok := r.companies[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrProfileNotFound, id)
	}
	company, err := domain.RehydrateCompany(snapshot)
	if err != nil {
		return nil, fmt.Errorf("memory: восстановление карточки %s: %w", id, err)
	}
	return company, nil
}

// ListVisible возвращает не скрытые карточки, новые первыми, не более limit
// штук. Порядок детерминирован по (CreatedAt, ID), поэтому повторный вызов даёт
// тот же состав и порядок.
func (r *LeadProfileRepository) ListVisible(_ context.Context, limit int) ([]*domain.Company, error) {
	if limit <= 0 {
		return []*domain.Company{}, nil
	}

	r.mu.RLock()
	snapshots := make([]domain.CompanySnapshot, 0, len(r.companies))
	for _, snapshot := range r.companies {
		if snapshot.Hidden {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	r.mu.RUnlock()

	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].CreatedAt.Equal(snapshots[j].CreatedAt) {
			return snapshots[i].ID < snapshots[j].ID
		}
		return snapshots[i].CreatedAt.After(snapshots[j].CreatedAt)
	})
	if len(snapshots) > limit {
		snapshots = snapshots[:limit]
	}

	companies := make([]*domain.Company, 0, len(snapshots))
	for _, snapshot := range snapshots {
		company, err := domain.RehydrateCompany(snapshot)
		if err != nil {
			return nil, fmt.Errorf("memory: восстановление карточки %s: %w", snapshot.ID, err)
		}
		companies = append(companies, company)
	}
	return companies, nil
}

// ListSnoozed возвращает отложенных лидов (snooze_until != nil), новые первыми.
// Скрытые карточки не возвращаются.
func (r *LeadProfileRepository) ListSnoozed(_ context.Context) ([]*domain.Company, error) {
	r.mu.RLock()
	snapshots := make([]domain.CompanySnapshot, 0)
	for _, snapshot := range r.companies {
		if snapshot.Hidden || snapshot.Status.SnoozeUntil == nil {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	r.mu.RUnlock()

	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].CreatedAt.Equal(snapshots[j].CreatedAt) {
			return snapshots[i].ID < snapshots[j].ID
		}
		return snapshots[i].CreatedAt.After(snapshots[j].CreatedAt)
	})

	companies := make([]*domain.Company, 0, len(snapshots))
	for _, snapshot := range snapshots {
		company, err := domain.RehydrateCompany(snapshot)
		if err != nil {
			return nil, fmt.Errorf("memory: восстановление карточки %s: %w", snapshot.ID, err)
		}
		companies = append(companies, company)
	}
	return companies, nil
}

// Delete удаляет карточку безвозвратно. Удаление отсутствующей карточки — не
// ошибка (операция идемпотентна).
func (r *LeadProfileRepository) Delete(_ context.Context, id domain.CompanyID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.companies, id)
	return nil
}
