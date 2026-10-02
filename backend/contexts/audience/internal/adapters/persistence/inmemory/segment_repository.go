// Package inmemory реализует порты репозиториев контекста audience поверх
// оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.SegmentSnapshot, domain.AudienceSnapshotState), а загрузка идёт через
// domain.RehydrateSegment и domain.RehydrateAudienceSnapshot. Поэтому сохранённый
// агрегат изолирован от вызывающего: последующие правки не меняют уже сохранённое
// состояние, а загрузка заново проверяет инварианты. Реализация не знает о
// транзакциях, драйверах и SQL — атомарностью записи управляет unit-of-work
// адаптера outbox/inmemory.
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

// SegmentRepository — in-memory реализация ports.SegmentRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type SegmentRepository struct {
	mu       sync.RWMutex
	segments map[domain.SegmentID]domain.SegmentSnapshot
}

// SegmentRepository реализует порт репозитория сегментов.
var _ ports.SegmentRepository = (*SegmentRepository)(nil)

// NewSegmentRepository создаёт пустой репозиторий сегментов.
func NewSegmentRepository() *SegmentRepository {
	return &SegmentRepository{segments: make(map[domain.SegmentID]domain.SegmentSnapshot)}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
func (r *SegmentRepository) Save(_ context.Context, segment *domain.Segment, expectedVersion int) error {
	if segment == nil {
		return errors.New("inmemory: сохранение nil-сегмента")
	}
	snapshot := segment.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.segments[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: сегмент %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: сегмент %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.segments[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает сегмент по идентификатору через domain.RehydrateSegment.
func (r *SegmentRepository) FindByID(_ context.Context, id domain.SegmentID) (*domain.Segment, error) {
	r.mu.RLock()
	snapshot, ok := r.segments[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrSegmentNotFound, id)
	}
	segment, err := domain.RehydrateSegment(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление сегмента %s: %w", id, err)
	}
	return segment, nil
}

// ListByTenant возвращает сегменты тенанта, упорядоченные по идентификатору.
func (r *SegmentRepository) ListByTenant(ctx context.Context, tenant domain.TenantID) ([]*domain.Segment, error) {
	ids := r.ids(func(snapshot domain.SegmentSnapshot) bool { return snapshot.TenantID == tenant })
	return r.load(ctx, ids)
}

// List возвращает все сегменты, упорядоченные по идентификатору.
func (r *SegmentRepository) List(ctx context.Context) ([]*domain.Segment, error) {
	ids := r.ids(func(domain.SegmentSnapshot) bool { return true })
	return r.load(ctx, ids)
}

// ids возвращает отфильтрованные идентификаторы сегментов в порядке возрастания.
func (r *SegmentRepository) ids(keep func(domain.SegmentSnapshot) bool) []domain.SegmentID {
	r.mu.RLock()
	ids := make([]domain.SegmentID, 0, len(r.segments))
	for id, snapshot := range r.segments {
		if keep(snapshot) {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// load загружает сегменты по идентификаторам.
func (r *SegmentRepository) load(ctx context.Context, ids []domain.SegmentID) ([]*domain.Segment, error) {
	segments := make([]*domain.Segment, 0, len(ids))
	for _, id := range ids {
		segment, err := r.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *SegmentRepository) Clone() *SegmentRepository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := NewSegmentRepository()
	for id, snapshot := range r.segments {
		clone.segments[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit.
func (r *SegmentRepository) ReplaceWith(other *SegmentRepository) {
	other.mu.RLock()
	snapshots := make(map[domain.SegmentID]domain.SegmentSnapshot, len(other.segments))
	for id, snapshot := range other.segments {
		snapshots[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.segments = snapshots
	r.mu.Unlock()
}
