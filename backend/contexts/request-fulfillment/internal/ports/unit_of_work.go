package ports

import (
	"context"
	"errors"
)

// ErrNestedTransaction сообщает, что UoW вызван из callback уже активной
// транзакции. Вложенный вызов не берёт второй lock и не открывает transaction.
var ErrNestedTransaction = errors.New("порты: вложенная транзакция не поддерживается")

type transactionScopeKey struct{}

// WithTransactionScope создаёт derived context callback-а с маркером активной
// транзакции. Вложенный UoW, получивший этот context или его производный, должен
// вернуть ErrNestedTransaction до захвата gate/открытия БД.
func WithTransactionScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, transactionScopeKey{}, true)
}

// HasTransactionScope сообщает, что context происходит из активного callback.
func HasTransactionScope(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	active, _ := ctx.Value(transactionScopeKey{}).(bool)
	return active
}

// Transaction — доступ к портам в рамках одной активной транзакции.
//
// Репозиторий и outbox, полученные из Transaction, привязаны к этой транзакции,
// поэтому запись агрегата и запись его событий выполняются атомарно.
type Transaction interface {
	// Requests возвращает репозиторий заявок в этой транзакции.
	Requests() RequestRepository

	// Outbox возвращает outbox этой транзакции.
	Outbox() Outbox

	// ExecutionAttempts возвращает durable owner store этой транзакции. SaveIntent
	// выполняется вместе с RequestRepository и Outbox в одном commit.
	ExecutionAttempts() ExecutionAttemptRepository
	// ExecutionResolutions сохраняет append-only resolution в той же транзакции.
	ExecutionResolutions() ExecutionResolutionRepository
	// VerifiedRuntimeResolutions is a narrow transactional capability. It is not
	// exposed through HTTP dependencies; application reconciliation code obtains
	// it only inside this UoW callback.
	VerifiedRuntimeResolutions() VerifiedRuntimeResolutionWriter
}

// UnitOfWork — порт транзакции: выполнение функции в одной транзакции.
//
// Callback получает txCtx, производный от caller context и помеченный активной
// транзакцией. Он обязан использовать txCtx для всех вызовов портов и не должен
// использовать исходный context/context.Background или выполнять внешний I/O.
// Если callback сам отбросит txCtx, generic Go API не может гарантированно
// обнаружить повторное обращение к repository. Callback ограничен короткой
// локальной работой; cancellation после его начала не останавливает callback,
// не сотрудничающий с context. Callback с ошибкой/panic не публикует состояние;
// повторный UoW с txCtx или производным context возвращает ErrNestedTransaction
// без ожидания gate.
type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(txCtx context.Context, tx Transaction) error) error
}
