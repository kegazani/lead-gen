// Package fulfillment реализует синхронный query-контракт чтения статусов
// артефактов фулфилмента (sync/http-json): HTTP-адаптер владельца и реализации
// для тестов и сборки. Заявка не импортирует модель владельца — она получает
// лишь статус артефакта по идентификатору.
package fulfillment

import (
	"context"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// MemoryQuery — реализация ports.FulfillmentQuery поверх оперативной памяти.
// Годится для тестов и локального запуска: статусы задаются заранее.
type MemoryQuery struct {
	mu       sync.RWMutex
	statuses map[ports.ArtifactRef]ports.ArtifactStatus
}

// NewMemoryQuery создаёт пустой запрос.
func NewMemoryQuery() *MemoryQuery {
	return &MemoryQuery{statuses: make(map[ports.ArtifactRef]ports.ArtifactStatus)}
}

// MemoryQuery реализует ports.FulfillmentQuery.
var _ ports.FulfillmentQuery = (*MemoryQuery)(nil)

// Set задаёт статус артефакта по ссылке. Возвращается тот же запрос для цепочки
// вызовов при конфигурации.
func (q *MemoryQuery) Set(ref ports.ArtifactRef, status string, found bool) *MemoryQuery {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.statuses[ref] = ports.ArtifactStatus{Ref: ref, Status: status, Found: found}
	return q
}

// Status возвращает заданный статус артефакта. Если статус не задан, артефакт
// считается не найденным: найденность подтверждает владелец, а не догадка.
func (q *MemoryQuery) Status(_ context.Context, ref ports.ArtifactRef) (ports.ArtifactStatus, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if status, ok := q.statuses[ref]; ok {
		return status, nil
	}
	return ports.ArtifactStatus{Ref: ref, Found: false}, nil
}

// Unavailable — реализация ports.FulfillmentQuery, которая всегда отвечает
// отказом чтения. Она честно сообщает, что контракт владельца не подключён, а не
// выдумывает статусы артефактов.
type Unavailable struct{}

// Unavailable реализует ports.FulfillmentQuery.
var _ ports.FulfillmentQuery = Unavailable{}

// Status возвращает отказ чтения.
func (Unavailable) Status(_ context.Context, ref ports.ArtifactRef) (ports.ArtifactStatus, error) {
	return ports.ArtifactStatus{}, fmt.Errorf("%w: %s:%s", ports.ErrArtifactUnavailable, ref.Kind, ref.ID)
}
