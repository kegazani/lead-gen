package ports

import "context"

// Transaction — доступ к портам в рамках одной активной транзакции.
//
// Репозитории и outbox, полученные из Transaction, привязаны к этой транзакции,
// поэтому запись агрегата и запись его событий выполняются атомарно.
type Transaction interface {
	// Pipelines возвращает репозиторий пайплайнов в этой транзакции.
	Pipelines() PipelineRepository

	// Runs возвращает репозиторий запусков в этой транзакции.
	Runs() RunRepository

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
