// Package inmemory реализует ports.TenantRepository поверх оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.TenantSnapshot), а загрузка идёт через domain.RehydrateTenant. Поэтому
// сохранённый агрегат изолирован от вызывающего: последующие правки тенанта не
// меняют уже сохранённое состояние, а загрузка заново проверяет инварианты.
// Реализация не знает о транзакциях, драйверах и SQL.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// Repository — in-memory реализация ports.TenantRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и параллельных читателей.
type Repository struct {
	mu      sync.RWMutex
	tenants map[domain.TenantID]domain.TenantSnapshot
}

// Repository реализует порт репозитория тенантов.
var _ ports.TenantRepository = (*Repository)(nil)

// New создаёт пустой репозиторий.
func New() *Repository {
	return &Repository{tenants: make(map[domain.TenantID]domain.TenantSnapshot)}
}

// Save сохраняет агрегат целиком как снимок. Повторное сохранение тенанта
// заменяет прежний снимок (upsert по идентификатору).
func (r *Repository) Save(_ context.Context, tenant *domain.Tenant) error {
	if tenant == nil {
		return errors.New("inmemory: сохранение nil-тенанта")
	}
	snapshot := tenant.Snapshot()
	r.mu.Lock()
	r.tenants[snapshot.ID] = snapshot
	r.mu.Unlock()
	return nil
}

// Clone возвращает независимую копию состояния репозитория. Копия нужна
// unit-of-work: транзакция работает с копией, а основное состояние заменяется
// только при успешном commit. Снимки тенантов трактуются как неизменяемые,
// поэтому копируется карта, а не сами снимки.
func (r *Repository) Clone() *Repository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	copyMap := make(map[domain.TenantID]domain.TenantSnapshot, len(r.tenants))
	for id, snapshot := range r.tenants {
		copyMap[id] = snapshot
	}
	return &Repository{tenants: copyMap}
}

// ReplaceWith заменяет состояние репозитория состоянием другого репозитория.
// Вызывается unit-of-work после успешного commit транзакции.
func (r *Repository) ReplaceWith(other *Repository) {
	if other == nil {
		return
	}
	other.mu.RLock()
	copyMap := make(map[domain.TenantID]domain.TenantSnapshot, len(other.tenants))
	for id, snapshot := range other.tenants {
		copyMap[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.tenants = copyMap
	r.mu.Unlock()
}

// FindByID загружает тенант по идентификатору через domain.RehydrateTenant.
// Отсутствие тенанта даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrTenantNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.TenantID) (*domain.Tenant, error) {
	r.mu.RLock()
	snapshot, ok := r.tenants[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrTenantNotFound, id)
	}
	tenant, err := domain.RehydrateTenant(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление тенанта %s: %w", id, err)
	}
	return tenant, nil
}
