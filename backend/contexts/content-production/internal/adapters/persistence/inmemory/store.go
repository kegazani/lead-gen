// Package inmemory реализует порты хранилища контекста content-production поверх
// оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.ContentPackageSnapshot, domain.RenderJobSnapshot), а загрузка идёт через
// Rehydrate-функции домена. Поэтому сохранённый агрегат изолирован от
// вызывающего: последующие правки не меняют уже сохранённое состояние, а загрузка
// заново проверяет инварианты. Реализация не знает о транзакциях, драйверах и SQL
// — атомарностью записи управляет unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// Store — разделяемое in-memory состояние контент-пакетов и заданий рендеринга.
//
// Состояние защищено мьютексом, поэтому хранилище пригодно для `go test -race` и
// параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type Store struct {
	mu       sync.RWMutex
	packages map[domain.ContentAssetID]domain.ContentPackageSnapshot
	jobs     map[domain.RenderJobID]domain.RenderJobSnapshot
}

// New создаёт пустое хранилище.
func New() *Store {
	return &Store{
		packages: make(map[domain.ContentAssetID]domain.ContentPackageSnapshot),
		jobs:     make(map[domain.RenderJobID]domain.RenderJobSnapshot),
	}
}

// Content возвращает репозиторий контент-пакетов поверх хранилища.
func (s *Store) Content() *ContentRepository { return &ContentRepository{store: s} }

// RenderJobs возвращает репозиторий заданий рендеринга поверх хранилища.
func (s *Store) RenderJobs() *RenderJobRepository { return &RenderJobRepository{store: s} }

// Clone возвращает независимую копию хранилища для одной транзакции.
func (s *Store) Clone() *Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clone := New()
	for id, snapshot := range s.packages {
		clone.packages[id] = snapshot
	}
	for id, snapshot := range s.jobs {
		clone.jobs[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние хранилища состоянием другого. Вызывается
// unit-of-work в момент commit: рабочая копия транзакции становится основным
// состоянием. При откате ReplaceWith не вызывается, и копия отбрасывается.
func (s *Store) ReplaceWith(other *Store) {
	other.mu.RLock()
	packages := make(map[domain.ContentAssetID]domain.ContentPackageSnapshot, len(other.packages))
	for id, snapshot := range other.packages {
		packages[id] = snapshot
	}
	jobs := make(map[domain.RenderJobID]domain.RenderJobSnapshot, len(other.jobs))
	for id, snapshot := range other.jobs {
		jobs[id] = snapshot
	}
	other.mu.RUnlock()

	s.mu.Lock()
	s.packages = packages
	s.jobs = jobs
	s.mu.Unlock()
}

// ContentRepository — in-memory реализация ports.ContentPackageRepository.
type ContentRepository struct {
	store *Store
}

// ContentRepository реализует порт репозитория контент-пакетов.
var _ ports.ContentPackageRepository = (*ContentRepository)(nil)

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
func (r *ContentRepository) Save(_ context.Context, pkg *domain.ContentPackage, expectedVersion int) error {
	if pkg == nil {
		return errors.New("inmemory: сохранение nil-контент-пакета")
	}
	snapshot := pkg.Snapshot()
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if stored, ok := r.store.packages[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf("%w: пакет %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf("%w: пакет %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion)
	}
	r.store.packages[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает пакет по идентификатору через domain.RehydrateContentPackage.
func (r *ContentRepository) FindByID(_ context.Context, id domain.ContentAssetID) (*domain.ContentPackage, error) {
	r.store.mu.RLock()
	snapshot, ok := r.store.packages[id]
	r.store.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrContentPackageNotFound, id)
	}
	pkg, err := domain.RehydrateContentPackage(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление пакета %s: %w", id, err)
	}
	return pkg, nil
}

// RenderJobRepository — in-memory реализация ports.RenderJobRepository.
type RenderJobRepository struct {
	store *Store
}

// RenderJobRepository реализует порт репозитория заданий рендеринга.
var _ ports.RenderJobRepository = (*RenderJobRepository)(nil)

// Save сохраняет задание целиком как снимок с оптимистичной блокировкой.
func (r *RenderJobRepository) Save(_ context.Context, job *domain.RenderJob, expectedVersion int) error {
	if job == nil {
		return errors.New("inmemory: сохранение nil-задания рендеринга")
	}
	snapshot := job.Snapshot()
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if stored, ok := r.store.jobs[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf("%w: задание %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf("%w: задание %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion)
	}
	r.store.jobs[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает задание по идентификатору через domain.RehydrateRenderJob.
func (r *RenderJobRepository) FindByID(_ context.Context, id domain.RenderJobID) (*domain.RenderJob, error) {
	r.store.mu.RLock()
	snapshot, ok := r.store.jobs[id]
	r.store.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrRenderJobNotFound, id)
	}
	job, err := domain.RehydrateRenderJob(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление задания %s: %w", id, err)
	}
	return job, nil
}

// ListPending возвращает задания в состояниях requested и rendering,
// упорядоченные по идентификатору.
func (r *RenderJobRepository) ListPending(_ context.Context, limit int) ([]*domain.RenderJob, error) {
	r.store.mu.RLock()
	ids := make([]domain.RenderJobID, 0, len(r.store.jobs))
	for id, snapshot := range r.store.jobs {
		if snapshot.State == domain.RenderStateRequested || snapshot.State == domain.RenderStateRendering {
			ids = append(ids, id)
		}
	}
	r.store.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}

	jobs := make([]*domain.RenderJob, 0, len(ids))
	for _, id := range ids {
		job, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
