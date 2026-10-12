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
//
// Карточка хранится вместе с тенантом-владельцем: в выборку попадают только
// записи активного тенанта (см. ports.TenantFrom). Пустая область — тенант ”.
type LeadProfileRepository struct {
	mu        sync.RWMutex
	companies map[domain.CompanyID]domain.CompanySnapshot
	tenants   map[domain.CompanyID]string
}

// LeadProfileRepository реализует порт репозитория карточек.
var _ ports.LeadProfileRepository = (*LeadProfileRepository)(nil)

// NewLeadProfileRepository создаёт пустой репозиторий.
func NewLeadProfileRepository() *LeadProfileRepository {
	return &LeadProfileRepository{
		companies: make(map[domain.CompanyID]domain.CompanySnapshot),
		tenants:   make(map[domain.CompanyID]string),
	}
}

// Save сохраняет карточку целиком как снимок в тенанте активной области хранения.
// Повторный вызов для той же компании заменяет прежний снимок (upsert по
// идентификатору).
func (r *LeadProfileRepository) Save(ctx context.Context, company *domain.Company) error {
	if company == nil {
		return errors.New("memory: сохранение nil-карточки")
	}
	snapshot := company.Snapshot()
	tenant := ports.TenantFrom(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.companies[snapshot.ID] = snapshot
	r.tenants[snapshot.ID] = tenant
	return nil
}

// FindByID загружает карточку по идентификатору в активном тенанте через
// domain.RehydrateCompany. Карточка чужого тенанта недостижима: для неё ошибка
// удовлетворяет errors.Is(err, ports.ErrProfileNotFound).
func (r *LeadProfileRepository) FindByID(ctx context.Context, id domain.CompanyID) (*domain.Company, error) {
	tenant := ports.TenantFrom(ctx)
	r.mu.RLock()
	snapshot, ok := r.companies[id]
	stored := r.tenants[id]
	r.mu.RUnlock()
	if !ok || stored != tenant {
		return nil, fmt.Errorf("%w: %s", ports.ErrProfileNotFound, id)
	}
	company, err := domain.RehydrateCompany(snapshot)
	if err != nil {
		return nil, fmt.Errorf("memory: восстановление карточки %s: %w", id, err)
	}
	return company, nil
}

// ListVisible возвращает не скрытые карточки активного тенанта, новые первыми,
// не более limit штук. Порядок детерминирован по (CreatedAt, ID), поэтому
// повторный вызов даёт тот же состав и порядок.
func (r *LeadProfileRepository) ListVisible(ctx context.Context, limit int) ([]*domain.Company, error) {
	if limit <= 0 {
		return []*domain.Company{}, nil
	}
	tenant := ports.TenantFrom(ctx)

	r.mu.RLock()
	snapshots := make([]domain.CompanySnapshot, 0, len(r.companies))
	for id, snapshot := range r.companies {
		if snapshot.Hidden || r.tenants[id] != tenant {
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

// ListSnoozed возвращает отложенных лидов активного тенанта (snooze_until != nil),
// новые первыми. Скрытые карточки и карточки чужого тенанта не возвращаются.
func (r *LeadProfileRepository) ListSnoozed(ctx context.Context) ([]*domain.Company, error) {
	tenant := ports.TenantFrom(ctx)
	r.mu.RLock()
	snapshots := make([]domain.CompanySnapshot, 0)
	for id, snapshot := range r.companies {
		if snapshot.Hidden || snapshot.Status.SnoozeUntil == nil || r.tenants[id] != tenant {
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

// Delete удаляет карточку безвозвратно в активном тенанте. Удаление отсутствующей
// карточки или карточки чужого тенанта — не ошибка (операция идемпотентна и не
// пересекает границу тенанта).
func (r *LeadProfileRepository) Delete(ctx context.Context, id domain.CompanyID) error {
	tenant := ports.TenantFrom(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tenants[id] != tenant {
		return nil
	}
	delete(r.companies, id)
	delete(r.tenants, id)
	return nil
}
