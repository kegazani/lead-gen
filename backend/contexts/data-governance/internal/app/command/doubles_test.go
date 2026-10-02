package command_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// memoryRepo — минимальная реализация ports.ProcessingBasisRepository для тестов
// сценариев. Определена в тестовом пакете, чтобы app не зависел от adapters.
type memoryRepo struct {
	mu     sync.Mutex
	bases  map[domain.BasisID]domain.ProcessingBasisSnapshot
	tenant map[domain.BasisID]domain.TenantID
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{
		bases:  make(map[domain.BasisID]domain.ProcessingBasisSnapshot),
		tenant: make(map[domain.BasisID]domain.TenantID),
	}
}

func (r *memoryRepo) Save(_ context.Context, basis *domain.ProcessingBasis, expectedVersion int) error {
	if basis == nil {
		return fmt.Errorf("nil-основание")
	}
	snapshot := basis.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	if stored, ok := r.bases[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf("%w: сохранено %d, ожидалось %d", ports.ErrVersionConflict, stored.Version, expectedVersion)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf("%w: нет сохранённой версии", ports.ErrVersionConflict)
	}
	r.bases[snapshot.ID] = snapshot
	r.tenant[snapshot.ID] = snapshot.TenantID
	return nil
}

func (r *memoryRepo) FindByID(_ context.Context, id domain.BasisID) (*domain.ProcessingBasis, error) {
	r.mu.Lock()
	snapshot, ok := r.bases[id]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrBasisNotFound, id)
	}
	return domain.RehydrateProcessingBasis(snapshot)
}

func (r *memoryRepo) FindByTenant(_ context.Context, tenant domain.TenantID) ([]*domain.ProcessingBasis, error) {
	r.mu.Lock()
	ids := make([]domain.BasisID, 0, len(r.bases))
	for id, owner := range r.tenant {
		if owner == tenant {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	return r.load(ids)
}

func (r *memoryRepo) ListExpired(_ context.Context, at time.Time) ([]*domain.ProcessingBasis, error) {
	r.mu.Lock()
	ids := make([]domain.BasisID, 0, len(r.bases))
	for id, snapshot := range r.bases {
		if snapshot.Status == domain.BasisActive && !at.Before(snapshot.ValidUntil) {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	return r.load(ids)
}

func (r *memoryRepo) load(ids []domain.BasisID) ([]*domain.ProcessingBasis, error) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	bases := make([]*domain.ProcessingBasis, 0, len(ids))
	for _, id := range ids {
		basis, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		bases = append(bases, basis)
	}
	return bases, nil
}

// fixedClock — детерминированные часы.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// seqIDs — детерминированный генератор идентификаторов событий.
type seqIDs struct{ n int }

func (g *seqIDs) NewEventID() domain.EventID {
	g.n++
	return domain.EventID(fmt.Sprintf("evt-%d", g.n))
}
