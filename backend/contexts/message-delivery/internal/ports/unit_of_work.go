package ports

import "context"

// Transaction — доступ к портам в рамках одной активной транзакции.
//
// Репозитории и outbox, полученные из Transaction, привязаны к этой транзакции,
// поэтому запись агрегата и запись его событий выполняются атомарно: use case
// сохраняет сообщение и репутацию и добавляет события, не зная, какая транзакция
// их объединяет. Transaction — узкий фасад над транзакцией, а не тип драйвера БД.
type Transaction interface {
	// Messages возвращает репозиторий сообщений в этой транзакции.
	Messages() MessageRepository

	// Reputations возвращает репозиторий репутаций в этой транзакции.
	Reputations() SenderReputationRepository

	// Outbox возвращает outbox этой транзакции.
	Outbox() Outbox
}

// UnitOfWork — порт транзакции: выполнение функции в одной транзакции.
//
// Use case открывает транзакцию, в её контексте сохраняет агрегаты и пишет
// события, затем коммитит. Если fn вернула ошибку, транзакция откатывается и
// ни агрегат, ни события не сохраняются. Так репозитории и Outbox остаются
// независимыми портами, а их запись связывает транзакция.
type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
