package ports

import (
	"context"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Outbox — порт transactional outbox: добавление domain events в рамках
// активной транзакции.
//
// Outbox получается из транзакции unit-of-work и привязан к ней: добавленные
// события коммитятся атомарно вместе с агрегатом и не публикуются до
// успешного commit (docs/domain-model.md, «Transactional outbox»). Публикацию
// после commit выполняет отдельный механизм адаптера, а не этот порт. Порт не
// знает о брокере, очереди или схеме таблицы outbox.
type Outbox interface {
	// Append добавляет события в outbox текущей транзакции в порядке передачи.
	// Событие считается записанным только после commit транзакции; ошибка
	// означает отказ записи и ведёт к откату вместе с транзакцией.
	Append(ctx context.Context, events ...domain.DomainEvent) error
}
