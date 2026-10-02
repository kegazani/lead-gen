package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// SuccessFeeRepository — in-memory реализация ports.SuccessFeeRepository.
// Состояние защищено мьютексом; Clone и ReplaceWith обслуживают unit-of-work.
type SuccessFeeRepository struct {
	mu   sync.RWMutex
	fees map[domain.SuccessFeeID]domain.SuccessFeeSnapshot
}

// SuccessFeeRepository реализует порт репозитория вознаграждений.
var _ ports.SuccessFeeRepository = (*SuccessFeeRepository)(nil)

// NewSuccessFeeRepository создаёт пустой репозиторий.
func NewSuccessFeeRepository() *SuccessFeeRepository {
	return &SuccessFeeRepository{fees: make(map[domain.SuccessFeeID]domain.SuccessFeeSnapshot)}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
func (r *SuccessFeeRepository) Save(_ context.Context, fee *domain.SuccessFee, expectedVersion int) error {
	if fee == nil {
		return errors.New("inmemory: сохранение nil-вознаграждения")
	}
	snapshot := fee.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.fees[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: вознаграждение %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: вознаграждение %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.fees[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает вознаграждение по идентификатору через
// domain.RehydrateSuccessFee.
func (r *SuccessFeeRepository) FindByID(_ context.Context, id domain.SuccessFeeID) (*domain.SuccessFee, error) {
	r.mu.RLock()
	snapshot, ok := r.fees[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrSuccessFeeNotFound, id)
	}
	fee, err := domain.RehydrateSuccessFee(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление вознаграждения %s: %w", id, err)
	}
	return fee, nil
}

// FindByResultRef находит вознаграждение тенанта по ссылке на результат.
func (r *SuccessFeeRepository) FindByResultRef(ctx context.Context, tenantID domain.TenantID, ref domain.ResultRef) (*domain.SuccessFee, error) {
	r.mu.RLock()
	var found domain.SuccessFeeID
	ok := false
	for id, snapshot := range r.fees {
		if snapshot.TenantID != tenantID || snapshot.ResultRef != ref {
			continue
		}
		if !ok || id < found {
			found = id
			ok = true
		}
	}
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: результат %s", ports.ErrSuccessFeeNotFound, ref)
	}
	return r.FindByID(ctx, found)
}

// ListByTenant возвращает вознаграждения тенанта, упорядоченные по идентификатору.
func (r *SuccessFeeRepository) ListByTenant(_ context.Context, tenantID domain.TenantID) ([]*domain.SuccessFee, error) {
	r.mu.RLock()
	ids := make([]domain.SuccessFeeID, 0)
	for id, snapshot := range r.fees {
		if snapshot.TenantID == tenantID {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]*domain.SuccessFee, 0, len(ids))
	for _, id := range ids {
		fee, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		out = append(out, fee)
	}
	return out, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *SuccessFeeRepository) Clone() *SuccessFeeRepository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := NewSuccessFeeRepository()
	for id, snapshot := range r.fees {
		clone.fees[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого.
func (r *SuccessFeeRepository) ReplaceWith(other *SuccessFeeRepository) {
	other.mu.RLock()
	snapshots := make(map[domain.SuccessFeeID]domain.SuccessFeeSnapshot, len(other.fees))
	for id, snapshot := range other.fees {
		snapshots[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.fees = snapshots
	r.mu.Unlock()
}
