package ports

import (
	"context"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
)

// Outbox — порт transactional outbox: добавление domain events в рамках активной
// транзакции.
//
// События, добавленные через Outbox, коммитятся атомарно с локальным состоянием
// (синхронизацией сделки) и не публикуются до успешного commit. Публикацию после
// commit выполняет отдельный механизм адаптера. Порт не знает о брокере, очереди
// и схеме таблицы outbox.
type Outbox interface {
	// Append добавляет события в outbox текущей транзакции в порядке передачи.
	Append(ctx context.Context, events ...domain.DomainEvent) error
}
