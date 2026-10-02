package ports

import "context"

// Transaction — доступ к портам в рамках одной активной транзакции.
//
// Репозиторий и outbox, полученные из Transaction, привязаны к этой транзакции,
// поэтому запись агрегата и запись его событий выполняются атомарно.
type Transaction interface {
	// Requests возвращает репозиторий заявок в этой транзакции.
	Requests() RequestRepository

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
