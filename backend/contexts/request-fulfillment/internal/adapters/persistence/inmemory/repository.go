// Package inmemory реализует ports.RequestRepository поверх оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.RequestSnapshot), а загрузка идёт через domain.RehydrateRequest.
// Поэтому сохранённый агрегат изолирован от вызывающего: последующие правки
// заявки не меняют уже сохранённое состояние, а загрузка заново проверяет
// инварианты. Реализация не знает о транзакциях, драйверах и SQL — атомарностью
// записи управляет unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// Repository — in-memory реализация ports.RequestRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type Repository struct {
	mu       sync.RWMutex
	requests map[domain.RequestID]domain.RequestSnapshot
}

// Repository реализует порт репозитория заявок.
var _ ports.RequestRepository = (*Repository)(nil)

// New создаёт пустой репозиторий.
func New() *Repository {
	return &Repository{requests: make(map[domain.RequestID]domain.RequestSnapshot)}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// Если сохранённая версия отличается от expectedVersion, сохранение отвергается
// ошибкой ports.ErrVersionConflict и состояние не меняется. При создании заявки
// (отсутствующей в хранилище) expectedVersion равен нулю. Повторное сохранение
// той же заявки заменяет прежний снимок (upsert по идентификатору).
func (r *Repository) Save(_ context.Context, request *domain.Request, expectedVersion int) error {
	if request == nil {
		return errors.New("inmemory: сохранение nil-заявки")
	}
	snapshot := request.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.requests[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: заявка %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: заявка %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.requests[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает заявку по идентификатору через domain.RehydrateRequest.
// Отсутствие заявки даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrRequestNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.RequestID) (*domain.Request, error) {
	r.mu.RLock()
	snapshot, ok := r.requests[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrRequestNotFound, id)
	}
	request, err := domain.RehydrateRequest(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление заявки %s: %w", id, err)
	}
	return request, nil
}

// ListByStatus возвращает заявки в заданном статусе, упорядоченные по
// идентификатору. Обслуживает чтение очереди валидации (submitted).
func (r *Repository) ListByStatus(_ context.Context, status domain.RequestStatus) ([]*domain.Request, error) {
	r.mu.RLock()
	ids := make([]domain.RequestID, 0, len(r.requests))
	for id, snapshot := range r.requests {
		if snapshot.Status == status {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	requests := make([]*domain.Request, 0, len(ids))
	for _, id := range ids {
		request, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *Repository) Clone() *Repository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := New()
	for id, snapshot := range r.requests {
		clone.requests[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit: рабочая копия транзакции становится основным
// состоянием. При откате ReplaceWith не вызывается, и копия отбрасывается.
func (r *Repository) ReplaceWith(other *Repository) {
	other.mu.RLock()
	snapshots := make(map[domain.RequestID]domain.RequestSnapshot, len(other.requests))
	for id, snapshot := range other.requests {
		snapshots[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.requests = snapshots
	r.mu.Unlock()
}
