package inmemory

import (
	"context"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// IntegrationInvocationRepository — in-memory реализация
// ports.IntegrationInvocationRepository. Кроме хранения по идентификатору
// репозиторий ведёт индекс ключа идемпотентности и порядок добавления вызовов,
// чтобы возвращать последний вызов в correlation-ref.
type IntegrationInvocationRepository struct {
	mu    sync.RWMutex
	byID  map[domain.InvocationID]domain.IntegrationInvocationSnapshot
	byKey map[string]domain.InvocationID
	order []domain.InvocationID
}

var _ ports.IntegrationInvocationRepository = (*IntegrationInvocationRepository)(nil)

// NewIntegrationInvocationRepository создаёт пустой репозиторий.
func NewIntegrationInvocationRepository() *IntegrationInvocationRepository {
	return &IntegrationInvocationRepository{
		byID:  make(map[domain.InvocationID]domain.IntegrationInvocationSnapshot),
		byKey: make(map[string]domain.InvocationID),
	}
}

// Save сохраняет вызов как снимок (upsert по идентификатору) и поддерживает
// индекс ключа идемпотентности и порядок добавления.
func (r *IntegrationInvocationRepository) Save(_ context.Context, invocation *domain.IntegrationInvocation) error {
	if invocation == nil {
		return fmt.Errorf("inmemory: сохранение nil-вызова")
	}
	snapshot := invocation.Snapshot()
	key := invocation.IdempotencyKey()

	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.byID[snapshot.ID]; ok {
		previousKey := domain.InvocationKey(
			previous.TenantID, previous.IntegrationType, previous.Operation, previous.CommandKey,
		)
		if previousKey != key {
			delete(r.byKey, previousKey)
		}
	} else {
		r.order = append(r.order, snapshot.ID)
	}
	r.byID[snapshot.ID] = snapshot
	r.byKey[key] = snapshot.ID
	return nil
}

// FindByID загружает вызов по идентификатору. Отсутствие даёт
// ports.ErrInvocationNotFound; противоречивый снимок — доменную ошибку.
func (r *IntegrationInvocationRepository) FindByID(_ context.Context, id domain.InvocationID) (*domain.IntegrationInvocation, error) {
	r.mu.RLock()
	snapshot, ok := r.byID[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrInvocationNotFound, id)
	}
	return rehydrateInvocation(id, snapshot)
}

// FindByIdempotencyKey загружает вызов по ключу идемпотентности.
func (r *IntegrationInvocationRepository) FindByIdempotencyKey(_ context.Context, key string) (*domain.IntegrationInvocation, error) {
	r.mu.RLock()
	id, ok := r.byKey[key]
	if !ok {
		r.mu.RUnlock()
		return nil, fmt.Errorf("%w: ключ %q", ports.ErrInvocationNotFound, key)
	}
	snapshot := r.byID[id]
	r.mu.RUnlock()
	return rehydrateInvocation(id, snapshot)
}

// FindLatestByCorrelation загружает последний добавленный вызов в correlation-ref.
func (r *IntegrationInvocationRepository) FindLatestByCorrelation(_ context.Context, ref domain.CorrelationRef) (*domain.IntegrationInvocation, error) {
	r.mu.RLock()
	var found domain.IntegrationInvocationSnapshot
	var ok bool
	for i := len(r.order) - 1; i >= 0; i-- {
		snapshot := r.byID[r.order[i]]
		if snapshot.CorrelationRef == ref {
			found, ok = snapshot, true
			break
		}
	}
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: correlation-ref %s", ports.ErrInvocationNotFound, ref)
	}
	return rehydrateInvocation(found.ID, found)
}

// PutInvocationSnapshot кладёт снимок напрямую, минуя домен. Метод существует
// для contract suite и диагностики: он позволяет поместить в хранилище намеренно
// противоречивый снимок и проверить, что загрузка отличается от «нет сущности».
func (r *IntegrationInvocationRepository) PutInvocationSnapshot(snapshot domain.IntegrationInvocationSnapshot) error {
	key := domain.InvocationKey(snapshot.TenantID, snapshot.IntegrationType, snapshot.Operation, snapshot.CommandKey)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[snapshot.ID]; !ok {
		r.order = append(r.order, snapshot.ID)
	}
	r.byID[snapshot.ID] = snapshot
	r.byKey[key] = snapshot.ID
	return nil
}

func rehydrateInvocation(id domain.InvocationID, snapshot domain.IntegrationInvocationSnapshot) (*domain.IntegrationInvocation, error) {
	invocation, err := domain.RehydrateIntegrationInvocation(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление вызова %s: %w", id, err)
	}
	return invocation, nil
}
