// Package inmemory реализует ports.Outbox поверх оперативной памяти: события
// накапливаются в порядке подтверждения. Публикацию в брокер выполняет отдельный
// транспортный адаптер; в объёме среза он не подключён, и это не скрывается.
package inmemory

import (
	"context"
	"sync"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// Outbox — in-memory реализация ports.Outbox.
type Outbox struct {
	mu     sync.Mutex
	events []domain.DomainEvent
}

// Outbox реализует порт outbox.
var _ ports.Outbox = (*Outbox)(nil)

// New создаёт пустой outbox.
func New() *Outbox { return &Outbox{} }

// Append добавляет события в порядке передачи.
func (o *Outbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, events...)
	return nil
}

// Events возвращает копию накопленных событий. Метод нужен тестам и диагностике:
// интерфейс порта остаётся минимальным.
func (o *Outbox) Events() []domain.DomainEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]domain.DomainEvent, len(o.events))
	copy(out, o.events)
	return out
}
