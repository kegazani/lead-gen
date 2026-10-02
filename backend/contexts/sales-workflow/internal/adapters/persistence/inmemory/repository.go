// Package inmemory реализует ports.CRMSyncRepository поверх оперативной памяти.
//
// Адаптер хранит снимки состояния (domain.CRMSyncSnapshot), а загрузка идёт через
// domain.RehydrateCRMSync: сохранённый агрегат изолирован от вызывающего, а
// загрузка заново проверяет инварианты. Реализация не знает о транзакциях,
// драйверах и SQL — атомарностью записи управляет unit-of-work адаптера
// outbox/inmemory.
//
// Методы Clone и ReplaceWith существуют для unit-of-work: транзакция работает с
// копией, а commit переносит её состояние.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// Repository — in-memory реализация ports.CRMSyncRepository.
type Repository struct {
	mu     sync.RWMutex
	syncs  map[domain.CRMSyncID]domain.CRMSyncSnapshot
	byDeal map[domain.DealID]domain.CRMSyncID
}

// Repository реализует порт синхронизации.
var _ ports.CRMSyncRepository = (*Repository)(nil)

// New создаёт пустой репозиторий.
func New() *Repository {
	return &Repository{
		syncs:  make(map[domain.CRMSyncID]domain.CRMSyncSnapshot),
		byDeal: make(map[domain.DealID]domain.CRMSyncID),
	}
}

// Save сохраняет запись синхронизации (upsert по идентификатору).
func (r *Repository) Save(_ context.Context, sync *domain.CRMSync) error {
	if sync == nil {
		return errors.New("inmemory: сохранение nil-синхронизации")
	}
	snapshot := sync.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.syncs[snapshot.ID] = snapshot
	r.byDeal[snapshot.DealID] = snapshot.ID
	return nil
}

// FindByDeal загружает синхронизацию по сделке через domain.RehydrateCRMSync.
// Отсутствие записи даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrCRMSyncNotFound).
func (r *Repository) FindByDeal(_ context.Context, dealID domain.DealID) (*domain.CRMSync, error) {
	r.mu.RLock()
	id, ok := r.byDeal[dealID]
	var snapshot domain.CRMSyncSnapshot
	if ok {
		snapshot = r.syncs[id]
	}
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: сделка %s", ports.ErrCRMSyncNotFound, dealID)
	}
	sync, err := domain.RehydrateCRMSync(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление синхронизации %s: %w", id, err)
	}
	return sync, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *Repository) Clone() *Repository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := New()
	for id, snapshot := range r.syncs {
		clone.syncs[id] = snapshot
	}
	for deal, id := range r.byDeal {
		clone.byDeal[deal] = id
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit; при откате не вызывается, и копия отбрасывается.
func (r *Repository) ReplaceWith(other *Repository) {
	other.mu.RLock()
	syncs := make(map[domain.CRMSyncID]domain.CRMSyncSnapshot, len(other.syncs))
	for id, snapshot := range other.syncs {
		syncs[id] = snapshot
	}
	byDeal := make(map[domain.DealID]domain.CRMSyncID, len(other.byDeal))
	for deal, id := range other.byDeal {
		byDeal[deal] = id
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.syncs = syncs
	r.byDeal = byDeal
	r.mu.Unlock()
}
