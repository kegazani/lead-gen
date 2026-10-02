// Package inmemory реализует ports.PublicationRepository поверх оперативной
// памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.PublicationSnapshot), а загрузка идёт через domain.RehydratePublication.
// Поэтому сохранённый агрегат изолирован от вызывающего: последующие правки
// публикации не меняют уже сохранённое состояние, а загрузка заново проверяет
// инварианты. Реализация не знает о транзакциях, драйверах и SQL — атомарностью
// записи управляет unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Repository — in-memory реализация ports.PublicationRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type Repository struct {
	mu           sync.RWMutex
	publications map[domain.PublicationID]domain.PublicationSnapshot
}

// Repository реализует порт репозитория публикаций.
var _ ports.PublicationRepository = (*Repository)(nil)

// New создаёт пустой репозиторий.
func New() *Repository {
	return &Repository{publications: make(map[domain.PublicationID]domain.PublicationSnapshot)}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// Если сохранённая версия отличается от expectedVersion, сохранение отвергается
// ошибкой ports.ErrVersionConflict и состояние не меняется. При создании
// публикации (отсутствующей в хранилище) expectedVersion равен нулю.
func (r *Repository) Save(_ context.Context, p *domain.Publication, expectedVersion int) error {
	if p == nil {
		return errors.New("inmemory: сохранение nil-публикации")
	}
	snapshot := p.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.publications[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: публикация %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: публикация %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}
	r.publications[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает публикацию по идентификатору через
// domain.RehydratePublication. Отсутствие публикации даёт ошибку, для которой
// истинно errors.Is(err, ports.ErrPublicationNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.PublicationID) (*domain.Publication, error) {
	r.mu.RLock()
	snapshot, ok := r.publications[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrPublicationNotFound, id)
	}
	return rehydrate(snapshot)
}

// FindByIdempotencyKey загружает публикацию по ключу идемпотентности. Ключ
// уникален в хранилище: повтор команды с тем же ключом не создаёт вторую запись.
func (r *Repository) FindByIdempotencyKey(_ context.Context, key domain.IdempotencyKey) (*domain.Publication, error) {
	r.mu.RLock()
	var found *domain.PublicationSnapshot
	for _, snapshot := range r.publications {
		if snapshot.IdempotencyKey == key {
			s := snapshot
			found = &s
			break
		}
	}
	r.mu.RUnlock()
	if found == nil {
		return nil, fmt.Errorf("%w: ключ %s", ports.ErrPublicationNotFound, key)
	}
	return rehydrate(*found)
}

// ListByStatus возвращает публикации в заданном статусе, упорядоченные по
// идентификатору. Обслуживает чтение очереди (например, approved — к публикации).
func (r *Repository) ListByStatus(_ context.Context, status domain.PublicationStatus) ([]*domain.Publication, error) {
	r.mu.RLock()
	ids := make([]domain.PublicationID, 0, len(r.publications))
	for id, snapshot := range r.publications {
		if snapshot.Status == status {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	publications := make([]*domain.Publication, 0, len(ids))
	for _, id := range ids {
		p, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		publications = append(publications, p)
	}
	return publications, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *Repository) Clone() *Repository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := New()
	for id, snapshot := range r.publications {
		clone.publications[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit. При откате ReplaceWith не вызывается, и копия
// отбрасывается.
func (r *Repository) ReplaceWith(other *Repository) {
	other.mu.RLock()
	snapshots := make(map[domain.PublicationID]domain.PublicationSnapshot, len(other.publications))
	for id, snapshot := range other.publications {
		snapshots[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.publications = snapshots
	r.mu.Unlock()
}

// rehydrate восстанавливает агрегат из снимка, оборачивая доменную ошибку.
func rehydrate(snapshot domain.PublicationSnapshot) (*domain.Publication, error) {
	p, err := domain.RehydratePublication(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление публикации %s: %w", snapshot.ID, err)
	}
	return p, nil
}
