// Package inmemory реализует ports.ProcessingBasisRepository поверх оперативной
// памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.ProcessingBasisSnapshot), а загрузка идёт через
// domain.RehydrateProcessingBasis. Поэтому сохранённый агрегат изолирован от
// вызывающего: последующие правки основания не меняют уже сохранённое состояние,
// а загрузка заново проверяет инварианты. Реализация не знает о транзакциях,
// драйверах и SQL.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// Repository — in-memory реализация ports.ProcessingBasisRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для
// `go test -race` и параллельных читателей.
type Repository struct {
	mu     sync.RWMutex
	bases  map[domain.BasisID]domain.ProcessingBasisSnapshot
	tenant map[domain.BasisID]domain.TenantID
}

// Repository реализует порт репозитория оснований.
var _ ports.ProcessingBasisRepository = (*Repository)(nil)

// New создаёт пустой репозиторий.
func New() *Repository {
	return &Repository{
		bases:  make(map[domain.BasisID]domain.ProcessingBasisSnapshot),
		tenant: make(map[domain.BasisID]domain.TenantID),
	}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// Если сохранённая версия отличается от expectedVersion, сохранение отвергается
// ошибкой ports.ErrVersionConflict и состояние не меняется. При создании основания
// (отсутствующего в хранилище) expectedVersion равен нулю.
func (r *Repository) Save(_ context.Context, basis *domain.ProcessingBasis, expectedVersion int) error {
	if basis == nil {
		return errors.New("inmemory: сохранение nil-основания")
	}
	snapshot := basis.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.bases[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: основание %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: основание %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.bases[snapshot.ID] = snapshot
	r.tenant[snapshot.ID] = snapshot.TenantID
	return nil
}

// FindByID загружает основание по идентификатору через
// domain.RehydrateProcessingBasis. Отсутствие основания даёт ошибку, для которой
// истинно errors.Is(err, ports.ErrBasisNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.BasisID) (*domain.ProcessingBasis, error) {
	r.mu.RLock()
	snapshot, ok := r.bases[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrBasisNotFound, id)
	}
	basis, err := domain.RehydrateProcessingBasis(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление основания %s: %w", id, err)
	}
	return basis, nil
}

// FindByTenant возвращает все основания тенанта, упорядоченные по
// идентификатору, чтобы выдача была воспроизводимой.
func (r *Repository) FindByTenant(_ context.Context, tenant domain.TenantID) ([]*domain.ProcessingBasis, error) {
	r.mu.RLock()
	ids := make([]domain.BasisID, 0, len(r.bases))
	for id, owner := range r.tenant {
		if owner == tenant {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	return r.loadAll(ids)
}

// ListExpired возвращает действующие основания, окно действия которых истекло к
// моменту at.
func (r *Repository) ListExpired(_ context.Context, at time.Time) ([]*domain.ProcessingBasis, error) {
	r.mu.RLock()
	ids := make([]domain.BasisID, 0, len(r.bases))
	for id, snapshot := range r.bases {
		if snapshot.Status == domain.BasisActive && !at.Before(snapshot.ValidUntil) {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	return r.loadAll(ids)
}

// loadAll сортирует идентификаторы и восстанавливает агрегаты.
func (r *Repository) loadAll(ids []domain.BasisID) ([]*domain.ProcessingBasis, error) {
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
