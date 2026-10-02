package ports

import (
	"context"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
)

// Outbox — порт transactional outbox: добавление domain events, подтверждённых
// вместе с сохранением агрегата. Публикацию после подтверждения выполняет
// адаптер; порт не знает о брокере и схеме таблицы outbox.
type Outbox interface {
	// Append добавляет события в outbox в порядке передачи.
	Append(ctx context.Context, events ...domain.DomainEvent) error
}
