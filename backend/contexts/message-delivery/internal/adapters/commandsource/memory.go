package commandsource

import (
	"context"
	"errors"
	"sync"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// defaultBuffer — ёмкость очереди по умолчанию.
const defaultBuffer = 64

// ErrClosed — источник закрыт: новые команды не принимаются.
var ErrClosed = errors.New("commandsource: источник команд закрыт")

// Memory — in-memory реализация ports.CommandSource.
//
// Команды подаются в очередь методом Enqueue и выдаются методом Next в порядке
// подачи. Источник безопасен для конкурентного использования; закрытие
// завершает выдачу штатно (ok == false).
type Memory struct {
	mu     sync.Mutex
	ch     chan domain.DeliveryCommand
	closed bool
}

// Memory реализует порт источника команд.
var _ ports.CommandSource = (*Memory)(nil)

// New создаёт источник с буфером. Неположительный размер заменяется значением по
// умолчанию.
func New(buffer int) *Memory {
	if buffer <= 0 {
		buffer = defaultBuffer
	}
	return &Memory{ch: make(chan domain.DeliveryCommand, buffer)}
}

// Enqueue ставит команду в очередь. Закрытый источник отвергает команду, а не
// теряет её молча.
func (m *Memory) Enqueue(command domain.DeliveryCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.ch <- command
	return nil
}

// Next реализует ports.CommandSource. Возвращает false после закрытия источника;
// отмена контекста даёт ошибку контекста, а не ложную «команду нет».
func (m *Memory) Next(ctx context.Context) (domain.DeliveryCommand, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return domain.DeliveryCommand{}, false, ctx.Err()
	case command, ok := <-m.ch:
		if !ok {
			return domain.DeliveryCommand{}, false, nil
		}
		return command, true, nil
	}
}

// Close завершает выдачу команд. Повторный вызов безопасен.
func (m *Memory) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	close(m.ch)
}

// Len возвращает число команд, ожидающих выдачи. Вспомогательный метод для
// проверок и диагностики.
func (m *Memory) Len() int { return len(m.ch) }
