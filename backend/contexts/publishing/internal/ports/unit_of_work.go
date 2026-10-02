package ports

import (
	"context"

	"lead-gen/backend/contexts/publishing/internal/domain"
)

// Outbox — порт transactional outbox: добавление domain events в рамках активной
// транзакции.
//
// События, добавленные через Outbox, коммитятся атомарно вместе с агрегатом и не
// публикуются до успешного commit. Публикацию после commit выполняет отдельный
// механизм адаптера. Порт не знает о брокере, очереди и схеме таблицы outbox.
type Outbox interface {
	// Append добавляет события в outbox текущей транзакции в порядке передачи.
	Append(ctx context.Context, events ...domain.DomainEvent) error
}

// Transaction — доступ к портам в рамках одной активной транзакции.
type Transaction interface {
	// Publications возвращает репозиторий публикаций в этой транзакции.
	Publications() PublicationRepository

	// Outbox возвращает outbox этой транзакции.
	Outbox() Outbox
}

// UnitOfWork — порт транзакции: выполнение функции в одной транзакции.
//
// Use case открывает транзакцию, в её контексте сохраняет агрегат и пишет
// события, затем коммитит. Если fn вернула ошибку, транзакция откатывается и ни
// агрегат, ни события не сохраняются.
type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
