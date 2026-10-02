// Package inproc — in-process адаптер синхронного контракта delivery-command.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (deliverycommand.Port), с реализацией владельца: при co-location вызов идёт
// прямым вызовом в том же процессе, без сети и сериализации.
//
// # Почему адаптер лежит рядом с контрактом
//
// Адаптер знает конкретный контракт — тип deliverycommand.Command и его ключ
// идемпотентности, — поэтому его место рядом с контрактом, а не в
// platform/transport. platform — общий примитив, не знающий контрактов:
// положить туда адаптер значило бы заставить platform зависеть от published
// language конкретного владельца.
//
// # Семантика
//
// In-process адаптер сохраняет семантику контракта так, чтобы переход к
// сетевому адаптеру не менял наблюдаемое поведение: принимает context.Context и
// соблюдает дедлайн и отмену, а повторная доставка с тем же ключом
// идемпотентности применяется один раз.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"lead-gen/contracts/message-delivery/deliverycommand"
)

// defaultTimeout — бюджет вызова по умолчанию, если ни вызывающий, ни сборка не
// задали свой. Ноль означал бы «ждём сколько угодно», а синхронный вызов без
// границы недопустим: бюджет обязан быть у обеих сторон связи.
const defaultTimeout = 5 * time.Second

// Option настраивает адаптер при сборке.
type Option func(*Adapter)

// WithTimeout задаёт бюджет вызова. Неположительное значение заменяется
// значением по умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Adapter) { a.timeout = timeout }
}

// Adapter реализует deliverycommand.Port поверх реализации владельца. Адаптер
// безопасен для конкурентного использования.
type Adapter struct {
	impl    deliverycommand.Port
	timeout time.Duration

	mu      sync.Mutex
	applied map[string]struct{}
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
// Возвращённое значение — то, что видит потребитель; оно не даёт доступа к
// реализации владельца, кроме вызова контракта.
func Bind(impl deliverycommand.Port, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: delivery-command: не задана реализация владельца")
	}
	adapter := &Adapter{
		impl:    impl,
		timeout: defaultTimeout,
		applied: make(map[string]struct{}),
	}
	for _, option := range options {
		if option != nil {
			option(adapter)
		}
	}
	if adapter.timeout <= 0 {
		adapter.timeout = defaultTimeout
	}
	return adapter, nil
}

// Deliver реализует deliverycommand.Port.
//
// Порядок шагов: отмена контекста, проверка команды, дедупликация по ключу
// идемпотентности, затем вызов реализации владельца с бюджетом. Повторная
// доставка уже применённого ключа не вызывает владельца повторно. Ключ
// записывается только после успеха: неуспешный вызов не «съедается», и повтор
// вправе выполнить действие снова.
func (a *Adapter) Deliver(ctx context.Context, cmd deliverycommand.Command) (deliverycommand.Result, error) {
	if a == nil || a.impl == nil {
		return deliverycommand.Result{}, errors.New("inproc: delivery-command: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return deliverycommand.Result{}, err
	}
	if err := cmd.Validate(); err != nil {
		return deliverycommand.Result{}, err
	}

	key := cmd.IdempotencyKey()
	if a.appliedBefore(key) {
		return deliverycommand.Result{}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// Вызов идёт отдельной горутиной, чтобы отмена и дедлайн соблюдались даже
	// тогда, когда реализация владельца их не проверила: адаптер гарантирует
	// границу вызова так же, как это сделал бы сетевой клиент.
	type outcome struct {
		result deliverycommand.Result
		err    error
	}
	result := make(chan outcome, 1)
	go func() {
		res, err := a.impl.Deliver(callCtx, cmd)
		result <- outcome{result: res, err: err}
	}()

	select {
	case out := <-result:
		if out.err != nil {
			return deliverycommand.Result{}, out.err
		}
		a.markApplied(key)
		return out.result, nil
	case <-callCtx.Done():
		return deliverycommand.Result{}, fmt.Errorf("inproc: delivery-command: вызов прерван: %w", callCtx.Err())
	}
}

// appliedBefore сообщает, применялся ли ключ ранее.
func (a *Adapter) appliedBefore(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.applied[key]
	return ok
}

// markApplied фиксирует применённый ключ.
func (a *Adapter) markApplied(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applied[key] = struct{}{}
}

// Утверждение, что адаптер удовлетворяет потребительскому интерфейсу контракта.
var _ deliverycommand.Port = (*Adapter)(nil)
