// Package memory — in-memory адаптер порта внешней CRM для среза sales-workflow.
//
// Адаптер стенд-инит внешнюю CRM (зона red, storage external): он держит сделки в
// оперативной памяти процесса и не ходит в сеть. Реального сетевого адаптера CRM в
// срезе нет — это осознанная заглушка, а не рабочая интеграция; точка сборки
// заменяет её сетевым адаптером без изменения порта
// (internal/ports/deal_repository.go).
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.DealSnapshot), а загрузка идёт через domain.RehydrateDeal. Поэтому
// сохранённая сделка изолирована от вызывающего, а загрузка заново проверяет
// инварианты.
package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// Repository — in-memory реализация ports.DealRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и параллельных читателей.
type Repository struct {
	mu    sync.RWMutex
	deals map[domain.DealID]domain.DealSnapshot
}

// Repository реализует порт внешней CRM.
var _ ports.DealRepository = (*Repository)(nil)

// New создаёт пустой репозиторий.
func New() *Repository {
	return &Repository{deals: make(map[domain.DealID]domain.DealSnapshot)}
}

// Save сохраняет сделку целиком как снимок с оптимистичной блокировкой.
func (r *Repository) Save(_ context.Context, deal *domain.Deal, expectedVersion int) error {
	if deal == nil {
		return errors.New("crm/memory: сохранение nil-сделки")
	}
	snapshot := deal.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.deals[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: сделка %s: сохранено %d, ожидалось %d",
				ports.ErrDealVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: сделка %s: нет сохранённой версии, ожидалось %d",
			ports.ErrDealVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.deals[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает сделку по идентификатору через domain.RehydrateDeal.
// Отсутствие сделки даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrDealNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.DealID) (*domain.Deal, error) {
	r.mu.RLock()
	snapshot, ok := r.deals[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrDealNotFound, id)
	}
	deal, err := domain.RehydrateDeal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("crm/memory: восстановление сделки %s: %w", id, err)
	}
	return deal, nil
}

// ListByStage возвращает сделки в заданном этапе, упорядоченные по
// идентификатору.
func (r *Repository) ListByStage(_ context.Context, stage domain.DealStage) ([]*domain.Deal, error) {
	r.mu.RLock()
	ids := make([]domain.DealID, 0, len(r.deals))
	for id, snapshot := range r.deals {
		if snapshot.Stage == stage {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	deals := make([]*domain.Deal, 0, len(ids))
	for _, id := range ids {
		deal, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		deals = append(deals, deal)
	}
	return deals, nil
}
