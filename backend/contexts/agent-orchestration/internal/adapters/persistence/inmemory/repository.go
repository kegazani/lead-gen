// Package inmemory реализует порты хранилища контекста agent-orchestration поверх
// оперативной памяти: версии пайплайнов и запуски агентов.
//
// Адаптер хранит снимки состояния (domain.PipelineSnapshot, domain.RunSnapshot), а
// загрузка идёт через domain.Rehydrate*, поэтому сохранённый агрегат изолирован от
// вызывающего: последующие правки не меняют уже сохранённое состояние, а загрузка
// заново проверяет инварианты. Реализация не знает о транзакциях и SQL —
// атомарностью записи управляет unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// pipelineKey — ключ версии пайплайна: тенант, идентификатор и версия.
type pipelineKey struct {
	tenant  domain.TenantID
	id      domain.PipelineID
	version int
}

// Store — общее состояние хранилища: версии пайплайнов и запуски. Состояние
// защищено мьютексом, поэтому хранилище пригодно для `go test -race` и
// параллельных читателей. Методы Clone и ReplaceWith обслуживают unit-of-work.
type Store struct {
	mu        sync.RWMutex
	pipelines map[pipelineKey]domain.PipelineSnapshot
	runs      map[domain.RunID]domain.RunSnapshot
}

// NewStore создаёт пустое хранилище.
func NewStore() *Store {
	return &Store{
		pipelines: make(map[pipelineKey]domain.PipelineSnapshot),
		runs:      make(map[domain.RunID]domain.RunSnapshot),
	}
}

// Clone возвращает независимую копию хранилища для одной транзакции.
func (s *Store) Clone() *Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clone := NewStore()
	for key, snapshot := range s.pipelines {
		clone.pipelines[key] = snapshot
	}
	for id, snapshot := range s.runs {
		clone.runs[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние хранилища состоянием другого. Вызывается
// unit-of-work в момент commit.
func (s *Store) ReplaceWith(other *Store) {
	other.mu.RLock()
	pipelines := make(map[pipelineKey]domain.PipelineSnapshot, len(other.pipelines))
	for key, snapshot := range other.pipelines {
		pipelines[key] = snapshot
	}
	runs := make(map[domain.RunID]domain.RunSnapshot, len(other.runs))
	for id, snapshot := range other.runs {
		runs[id] = snapshot
	}
	other.mu.RUnlock()

	s.mu.Lock()
	s.pipelines = pipelines
	s.runs = runs
	s.mu.Unlock()
}

// Repositories — набор адаптеров хранилища, разделяющих одно состояние.
type Repositories struct {
	// Pipelines — репозиторий версий пайплайнов.
	Pipelines *PipelineRepository
	// Runs — репозиторий запусков.
	Runs *RunRepository

	store *Store
}

// Store возвращает общее хранилище набора (для unit-of-work).
func (r Repositories) Store() *Store { return r.store }

// New собирает набор адаптеров хранилища с общим состоянием.
func New() Repositories {
	store := NewStore()
	return Repositories{
		Pipelines: &PipelineRepository{store: store},
		Runs:      &RunRepository{store: store},
		store:     store,
	}
}

// NewPipelineRepository собирает репозиторий пайплайнов поверх хранилища. nil —
// новое хранилище.
func NewPipelineRepository(store *Store) *PipelineRepository {
	if store == nil {
		store = NewStore()
	}
	return &PipelineRepository{store: store}
}

// NewRunRepository собирает репозиторий запусков поверх хранилища. nil — новое
// хранилище.
func NewRunRepository(store *Store) *RunRepository {
	if store == nil {
		store = NewStore()
	}
	return &RunRepository{store: store}
}

// PipelineRepository — in-memory реализация ports.PipelineRepository.
type PipelineRepository struct {
	store *Store
}

// Реализация порта репозитория пайплайнов.
var _ ports.PipelineRepository = (*PipelineRepository)(nil)

// Save сохраняет версию пайплайна с оптимистичной блокировкой по ревизии.
func (r *PipelineRepository) Save(_ context.Context, pipeline *domain.AgentPipeline, expectedRevision int) error {
	if pipeline == nil {
		return errors.New("inmemory: сохранение nil-пайплайна")
	}
	snapshot := pipeline.Snapshot()
	key := pipelineKey{tenant: snapshot.TenantID, id: snapshot.ID, version: snapshot.Version}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if stored, ok := r.store.pipelines[key]; ok {
		if stored.Revision != expectedRevision {
			return fmt.Errorf("%w: пайплайн %s v%d: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, snapshot.Version, stored.Revision, expectedRevision)
		}
	} else if expectedRevision != 0 {
		return fmt.Errorf("%w: пайплайн %s v%d: нет сохранённой ревизии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, snapshot.Version, expectedRevision)
	}
	r.store.pipelines[key] = snapshot
	return nil
}

// FindByID загружает версию пайплайна тенанта.
func (r *PipelineRepository) FindByID(_ context.Context, id domain.PipelineID, version int, tenant domain.TenantID) (*domain.AgentPipeline, error) {
	key := pipelineKey{tenant: tenant, id: id, version: version}
	r.store.mu.RLock()
	snapshot, ok := r.store.pipelines[key]
	r.store.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s v%d", ports.ErrPipelineNotFound, id, version)
	}
	pipeline, err := domain.RehydratePipeline(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление пайплайна %s v%d: %w", id, version, err)
	}
	return pipeline, nil
}

// LatestPublished возвращает номер последней опубликованной версии пайплайна.
func (r *PipelineRepository) LatestPublished(_ context.Context, id domain.PipelineID, tenant domain.TenantID) (int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	latest := 0
	for key, snapshot := range r.store.pipelines {
		if key.tenant != tenant || key.id != id || !snapshot.Published {
			continue
		}
		if snapshot.Version > latest {
			latest = snapshot.Version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("%w: %s", ports.ErrPipelineNotFound, id)
	}
	return latest, nil
}

// RunRepository — in-memory реализация ports.RunRepository.
type RunRepository struct {
	store *Store
}

// Реализация порта репозитория запусков.
var _ ports.RunRepository = (*RunRepository)(nil)

// Save сохраняет запуск с оптимистичной блокировкой по ревизии.
func (r *RunRepository) Save(_ context.Context, run *domain.AgentRun, expectedRevision int) error {
	if run == nil {
		return errors.New("inmemory: сохранение nil-запуска")
	}
	snapshot := run.Snapshot()
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if stored, ok := r.store.runs[snapshot.ID]; ok {
		if stored.Revision != expectedRevision {
			return fmt.Errorf("%w: запуск %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Revision, expectedRevision)
		}
	} else if expectedRevision != 0 {
		return fmt.Errorf("%w: запуск %s: нет сохранённой ревизии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedRevision)
	}
	r.store.runs[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает запуск тенанта.
func (r *RunRepository) FindByID(_ context.Context, id domain.RunID, tenant domain.TenantID) (*domain.AgentRun, error) {
	r.store.mu.RLock()
	snapshot, ok := r.store.runs[id]
	r.store.mu.RUnlock()
	if !ok || snapshot.Mission.TenantID != tenant {
		return nil, fmt.Errorf("%w: %s", ports.ErrRunNotFound, id)
	}
	run, err := domain.RehydrateRun(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление запуска %s: %w", id, err)
	}
	return run, nil
}

// ListByStatus возвращает запуски тенанта в заданном статусе, упорядоченные по
// идентификатору.
func (r *RunRepository) ListByStatus(_ context.Context, status domain.RunStatus, tenant domain.TenantID, limit int) ([]*domain.AgentRun, error) {
	r.store.mu.RLock()
	ids := make([]domain.RunID, 0)
	for id, snapshot := range r.store.runs {
		if snapshot.Mission.TenantID == tenant && snapshot.Status == status {
			ids = append(ids, id)
		}
	}
	r.store.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	runs := make([]*domain.AgentRun, 0, len(ids))
	for _, id := range ids {
		run, err := r.FindByID(context.Background(), id, tenant)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// List возвращает запуски тенанта по фильтру, упорядоченные по времени
// последнего изменения по убыванию, затем по идентификатору по убыванию (лента
// консоли операций). Пустой статус — все статусы. Выборка без тенанта отвергается.
func (r *RunRepository) List(_ context.Context, filter ports.RunFilter) ([]*domain.AgentRun, error) {
	if filter.Tenant == "" {
		return nil, ports.ErrMissingTenant
	}
	type entry struct {
		id      domain.RunID
		updated time.Time
	}
	r.store.mu.RLock()
	entries := make([]entry, 0)
	for id, snapshot := range r.store.runs {
		if snapshot.Mission.TenantID != filter.Tenant {
			continue
		}
		if filter.Status != "" && snapshot.Status != filter.Status {
			continue
		}
		entries = append(entries, entry{id: id, updated: snapshot.UpdatedAt})
	}
	r.store.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].updated.Equal(entries[j].updated) {
			return entries[i].id > entries[j].id
		}
		return entries[i].updated.After(entries[j].updated)
	})
	if filter.Limit > 0 && len(entries) > filter.Limit {
		entries = entries[:filter.Limit]
	}

	runs := make([]*domain.AgentRun, 0, len(entries))
	for _, item := range entries {
		run, err := r.FindByID(context.Background(), item.id, filter.Tenant)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// CountActive возвращает число запусков тенанта, занимающих слот одновременной
// обработки (исполняются, ждут решения или заблокированы). Запуски в очереди слот
// не занимают.
func (r *RunRepository) CountActive(_ context.Context, tenant domain.TenantID) (int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	count := 0
	for _, snapshot := range r.store.runs {
		if snapshot.Mission.TenantID == tenant && snapshot.Status.OccupiesSlot() {
			count++
		}
	}
	return count, nil
}
