// Package inmemory реализует репозитории портов контекста integration-platform
// поверх оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.AdapterRegistrationSnapshot, domain.IntegrationInvocationSnapshot), а
// загрузка идёт через domain.Rehydrate*. Поэтому сохранённый агрегат изолирован
// от вызывающего, а загрузка заново проверяет инварианты. Реализация не знает о
// транзакциях, драйверах и SQL.
package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// AdapterRegistrationRepository — in-memory реализация
// ports.AdapterRegistrationRepository. Состояние защищено мьютексом.
type AdapterRegistrationRepository struct {
	mu     sync.RWMutex
	byID   map[domain.AdapterID]domain.AdapterRegistrationSnapshot
	byName map[domain.AdapterName]domain.AdapterID
}

var _ ports.AdapterRegistrationRepository = (*AdapterRegistrationRepository)(nil)

// NewAdapterRegistrationRepository создаёт пустой репозиторий.
func NewAdapterRegistrationRepository() *AdapterRegistrationRepository {
	return &AdapterRegistrationRepository{
		byID:   make(map[domain.AdapterID]domain.AdapterRegistrationSnapshot),
		byName: make(map[domain.AdapterName]domain.AdapterID),
	}
}

// Save сохраняет регистрацию как снимок. Повторный вызов для того же
// идентификатора заменяет прежний снимок (upsert), сохраняя уникальность имени.
func (r *AdapterRegistrationRepository) Save(_ context.Context, registration *domain.AdapterRegistration) error {
	if registration == nil {
		return fmt.Errorf("inmemory: сохранение nil-регистрации")
	}
	snapshot := registration.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.byID[snapshot.ID]; ok && previous.Manifest.AdapterName() != snapshot.Manifest.AdapterName() {
		delete(r.byName, previous.Manifest.AdapterName())
	}
	r.byID[snapshot.ID] = snapshot
	r.byName[snapshot.Manifest.AdapterName()] = snapshot.ID
	return nil
}

// FindByID загружает регистрацию по идентификатору через
// domain.RehydrateAdapterRegistration. Отсутствие даёт
// ports.ErrAdapterRegistrationNotFound; противоречивый снимок — доменную ошибку.
func (r *AdapterRegistrationRepository) FindByID(_ context.Context, id domain.AdapterID) (*domain.AdapterRegistration, error) {
	r.mu.RLock()
	snapshot, ok := r.byID[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrAdapterRegistrationNotFound, id)
	}
	return rehydrateRegistration(id, snapshot)
}

// FindByName загружает регистрацию по стабильному имени адаптера.
func (r *AdapterRegistrationRepository) FindByName(_ context.Context, name domain.AdapterName) (*domain.AdapterRegistration, error) {
	r.mu.RLock()
	id, ok := r.byName[name]
	if !ok {
		r.mu.RUnlock()
		return nil, fmt.Errorf("%w: адаптер %q", ports.ErrAdapterRegistrationNotFound, name)
	}
	snapshot := r.byID[id]
	r.mu.RUnlock()
	return rehydrateRegistration(id, snapshot)
}

// List возвращает регистрации, упорядоченные по идентификатору, — порядок
// детерминирован, чтобы выбор адаптера был воспроизводим.
func (r *AdapterRegistrationRepository) List(_ context.Context) ([]*domain.AdapterRegistration, error) {
	r.mu.RLock()
	snapshots := make([]domain.AdapterRegistrationSnapshot, 0, len(r.byID))
	for _, snapshot := range r.byID {
		snapshots = append(snapshots, snapshot)
	}
	r.mu.RUnlock()

	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })

	registrations := make([]*domain.AdapterRegistration, 0, len(snapshots))
	for _, snapshot := range snapshots {
		registration, err := rehydrateRegistration(snapshot.ID, snapshot)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}

// PutRegistrationSnapshot кладёт снимок напрямую, минуя домен. Метод существует
// для contract suite и диагностики: он позволяет поместить в хранилище намеренно
// противоречивый снимок и проверить, что загрузка отличается от «нет сущности».
func (r *AdapterRegistrationRepository) PutRegistrationSnapshot(snapshot domain.AdapterRegistrationSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[snapshot.ID] = snapshot
	r.byName[snapshot.Manifest.AdapterName()] = snapshot.ID
	return nil
}

func rehydrateRegistration(id domain.AdapterID, snapshot domain.AdapterRegistrationSnapshot) (*domain.AdapterRegistration, error) {
	registration, err := domain.RehydrateAdapterRegistration(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление регистрации %s: %w", id, err)
	}
	return registration, nil
}
