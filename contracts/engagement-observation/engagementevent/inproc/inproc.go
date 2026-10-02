// Package inproc — in-process адаптер контракта engagement-event.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (engagementevent.Publisher), с реализацией владельца: при co-location вызов идёт
// прямым вызовом в том же процессе, без сети и сериализации.
//
// # Почему адаптер лежит рядом с контрактом
//
// Адаптер знает конкретный контракт — тип engagementevent.EngagementEvent и его
// ключ идемпотентности, — поэтому его место рядом с контрактом, а не в
// platform/transport/inproc. platform — общий примитив, не знающий контрактов:
// положить туда адаптер значило бы заставить platform зависеть от published
// language конкретного владельца.
//
// # Семантика
//
// In-process адаптер обязан сохранять семантику контракта так, чтобы переход к
// сетевому адаптеру не менял наблюдаемое поведение (design.md, Решение 2):
//
//   - принимает context.Context и соблюдает дедлайн и отмену: вызов прерывается
//     по отмене контекста или по истечении бюджета;
//   - пробрасывает и соблюдает ключ идемпотентности: повторная доставка с тем же
//     ключом применяется один раз.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"lead-gen/contracts/engagement-observation/engagementevent"
)

// defaultTimeout — бюджет вызова по умолчанию, если ни вызывающий, ни сборка не
// задали свой. Синхронный вызов без границы недопустим.
const defaultTimeout = 5 * time.Second

// Option настраивает адаптер при сборке.
type Option func(*Adapter)

// WithTimeout задаёт бюджет вызова. Неположительное значение заменяется значением
// по умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Adapter) { a.timeout = timeout }
}

// Adapter реализует engagementevent.Publisher поверх реализации владельца.
// Адаптер безопасен для конкурентного использования.
type Adapter struct {
	impl    engagementevent.Publisher
	timeout time.Duration

	mu      sync.Mutex
	applied map[string]struct{}
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
// Возвращённое значение — то, что видит потребитель.
func Bind(impl engagementevent.Publisher, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: engagement-event: не задана реализация владельца")
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

// Publish реализует engagementevent.Publisher.
//
// Порядок шагов: отмена контекста, проверка контракта, дедупликация по ключу
// идемпотентности, затем вызов реализации владельца с бюджетом. Повторная
// доставка уже применённого ключа не вызывает владельца повторно. Ключ
// записывается только после успеха: неуспешный вызов не «съедается».
func (a *Adapter) Publish(ctx context.Context, event engagementevent.EngagementEvent) error {
	if a == nil || a.impl == nil {
		return errors.New("inproc: engagement-event: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return err
	}

	key := event.IdempotencyKey()
	if a.appliedBefore(key) {
		return nil
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- a.impl.Publish(callCtx, event)
	}()

	select {
	case err := <-result:
		if err != nil {
			return err
		}
		a.markApplied(key)
		return nil
	case <-callCtx.Done():
		return fmt.Errorf("inproc: engagement-event: вызов прерван: %w", callCtx.Err())
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
var _ engagementevent.Publisher = (*Adapter)(nil)
