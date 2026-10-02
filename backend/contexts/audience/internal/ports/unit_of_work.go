package ports

import "context"

// Transaction — доступ к портам в рамках одной активной транзакции.
//
// Репозитории и outbox, полученные из Transaction, привязаны к этой транзакции,
// поэтому запись агрегатов и запись их событий выполняются атомарно.
type Transaction interface {
	// Segments возвращает репозиторий сегментов в этой транзакции.
	Segments() SegmentRepository

	// Snapshots возвращает репозиторий снимков аудитории в этой транзакции.
	Snapshots() AudienceSnapshotRepository

	// Outbox возвращает outbox этой транзакции.
	Outbox() Outbox
}

// UnitOfWork — порт транзакции: выполнение функции в одной транзакции.
//
// Use case открывает транзакцию, в её контексте сохраняет агрегаты и пишет
// события, затем коммитит. Если fn вернула ошибку, транзакция откатывается и ни
// агрегаты, ни события не сохраняются.
type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
