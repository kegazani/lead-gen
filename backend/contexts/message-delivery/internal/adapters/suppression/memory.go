package suppression

import (
	"context"
	"sync"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Memory — in-memory реализация ports.SuppressionList для сборки и тестов.
//
// Список задаётся вызывающим: пустой стоп-лист означает «никто не заблокирован».
// Адаптер безопасен для конкурентного использования.
type Memory struct {
	mu         sync.RWMutex
	suppressed map[domain.DeliveryAddress]struct{}
}

// Memory реализует порт стоп-листа.
var _ ports.SuppressionList = (*Memory)(nil)

// New создаёт пустой стоп-лист.
func New() *Memory {
	return &Memory{suppressed: make(map[domain.DeliveryAddress]struct{})}
}

// Suppress добавляет адрес в стоп-лист. Возвращает адаптер для цепочки вызовов
// при конфигурации.
func (m *Memory) Suppress(address domain.DeliveryAddress) *Memory {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suppressed[address] = struct{}{}
	return m
}

// IsSuppressed реализует ports.SuppressionList.
func (m *Memory) IsSuppressed(_ context.Context, address domain.DeliveryAddress) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.suppressed[address]
	return ok, nil
}
