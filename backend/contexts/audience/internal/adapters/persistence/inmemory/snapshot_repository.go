package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// SnapshotRepository — in-memory реализация ports.AudienceSnapshotRepository.
//
// Снимок аудитории неизменяем, поэтому оптимистичной блокировки нет: Save —
// upsert по идентификатору. Состояние защищено мьютексом, поэтому репозиторий
// пригоден для `go test -race`. Методы Clone и ReplaceWith существуют для
// unit-of-work.
type SnapshotRepository struct {
	mu        sync.RWMutex
	snapshots map[domain.SnapshotID]domain.AudienceSnapshotState
}

// SnapshotRepository реализует порт репозитория снимков аудитории.
var _ ports.AudienceSnapshotRepository = (*SnapshotRepository)(nil)

// NewSnapshotRepository создаёт пустой репозиторий снимков.
func NewSnapshotRepository() *SnapshotRepository {
	return &SnapshotRepository{snapshots: make(map[domain.SnapshotID]domain.AudienceSnapshotState)}
}

// Save сохраняет зафиксированный снимок (upsert по идентификатору).
func (r *SnapshotRepository) Save(_ context.Context, snapshot *domain.AudienceSnapshot) error {
	if snapshot == nil {
		return errors.New("inmemory: сохранение nil-снимка")
	}
	state := snapshot.State()
	r.mu.Lock()
	r.snapshots[state.ID] = state
	r.mu.Unlock()
	return nil
}

// FindByID загружает снимок по идентификатору через domain.RehydrateAudienceSnapshot.
func (r *SnapshotRepository) FindByID(_ context.Context, id domain.SnapshotID) (*domain.AudienceSnapshot, error) {
	r.mu.RLock()
	state, ok := r.snapshots[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrSnapshotNotFound, id)
	}
	snapshot, err := domain.RehydrateAudienceSnapshot(state)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление снимка %s: %w", id, err)
	}
	return snapshot, nil
}

// ListBySegment возвращает снимки сегмента, упорядоченные по идентификатору.
func (r *SnapshotRepository) ListBySegment(ctx context.Context, segment domain.SegmentID) ([]*domain.AudienceSnapshot, error) {
	r.mu.RLock()
	ids := make([]domain.SnapshotID, 0, len(r.snapshots))
	for id, state := range r.snapshots {
		if state.Criteria.SegmentID() == segment {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	snapshots := make([]*domain.AudienceSnapshot, 0, len(ids))
	for _, id := range ids {
		snapshot, err := r.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *SnapshotRepository) Clone() *SnapshotRepository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := NewSnapshotRepository()
	for id, state := range r.snapshots {
		clone.snapshots[id] = state
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit.
func (r *SnapshotRepository) ReplaceWith(other *SnapshotRepository) {
	other.mu.RLock()
	states := make(map[domain.SnapshotID]domain.AudienceSnapshotState, len(other.snapshots))
	for id, state := range other.snapshots {
		states[id] = state
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.snapshots = states
	r.mu.Unlock()
}
