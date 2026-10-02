package ports

import (
	"context"
)

// Transaction — набор хранилищ и журналов одной транзакции. Прикладной слой
// работает только с этим фасадом и не знает, чем именно реализованы хранилища.
type Transaction interface {
	// Engagements возвращает хранилище событий взаимодействия транзакции.
	Engagements() EngagementRepository
	// Signals возвращает журнал дедупликации сигналов транзакции.
	Signals() SignalLedger
	// Links возвращает реестр трекинг-ссылок транзакции.
	Links() TransitionLinks
	// Outbox возвращает буфер исходящих событий транзакции.
	Outbox() Outbox
}

// UnitOfWork — граница транзакции: изменение агрегата, отметка дедупликации,
// регистрация ссылки и запись событий выполняются вместе либо не выполняются
// вовсе.
type UnitOfWork interface {
	// WithinTransaction выполняет fn в одной транзакции. При ошибке изменения
	// отбрасываются, а накопленные события не публикуются.
	WithinTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
