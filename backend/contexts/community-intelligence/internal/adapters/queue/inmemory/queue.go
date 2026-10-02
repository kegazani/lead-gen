// Package inmemory реализует ports.CommandQueue поверх канала. Роль schedule
// кладёт команды чтения, роль worker их разбирает. Очередь не знает о брокере:
// при разделении сервисов ту же форму даёт адаптер транспорта.
package inmemory

import (
	"context"

	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// Queue — in-memory реализация ports.CommandQueue.
type Queue struct {
	commands chan ports.ReadCommand
}

// Queue реализует порт очереди команд.
var _ ports.CommandQueue = (*Queue)(nil)

// New создаёт небуферизованную очередь.
func New() *Queue {
	return &Queue{commands: make(chan ports.ReadCommand)}
}

// NewBuffered создаёт очередь с буфером заданного размера. Буфер удобен при
// сборке, когда schedule и worker запускаются раздельно.
func NewBuffered(size int) *Queue {
	if size < 0 {
		size = 0
	}
	return &Queue{commands: make(chan ports.ReadCommand, size)}
}

// Enqueue добавляет команду. При отменённом контексте команда не принимается.
func (q *Queue) Enqueue(ctx context.Context, cmd ports.ReadCommand) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case q.commands <- cmd:
		return nil
	}
}

// Dequeue возвращает очередную команду, блокируясь до её появления или отмены
// контекста. Второе значение — false, если контекст отменён.
func (q *Queue) Dequeue(ctx context.Context) (ports.ReadCommand, bool) {
	select {
	case <-ctx.Done():
		return ports.ReadCommand{}, false
	case cmd := <-q.commands:
		return cmd, true
	}
}
