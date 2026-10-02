// Package inproc — in-process адаптер контракта billing-result.
//
// Адаптер связывает владельца, публикующего результат биллинга
// (billingresult.Publisher), с получателем (billingresult.Subscriber): при
// co-location публикация идёт прямым вызовом в том же процессе.
//
// # Семантика
//
// Адаптер сохраняет семантику контракта так, чтобы переход к сетевому адаптеру
// не менял наблюдаемое поведение: соблюдает дедлайн и отмену context.Context и
// проверяет контракт до передачи получателю. Невалидный результат не доставляется.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/contracts/billing/billingresult"
)

// defaultTimeout — бюджет вызова по умолчанию.
const defaultTimeout = 5 * time.Second

// Option настраивает адаптер при сборке.
type Option func(*Adapter)

// WithTimeout задаёт бюджет вызова. Неположительное значение заменяется
// значением по умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Adapter) { a.timeout = timeout }
}

// Adapter реализует billingresult.Publisher поверх получателя.
type Adapter struct {
	subscriber billingresult.Subscriber
	timeout    time.Duration
}

// Bind связывает получателя с владельческим интерфейсом контракта.
func Bind(subscriber billingresult.Subscriber, options ...Option) (*Adapter, error) {
	if subscriber == nil {
		return nil, errors.New("inproc: billing-result: не задан получатель")
	}
	adapter := &Adapter{subscriber: subscriber, timeout: defaultTimeout}
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

// Publish реализует billingresult.Publisher.
func (a *Adapter) Publish(ctx context.Context, result billingresult.Result) error {
	if a == nil || a.subscriber == nil {
		return errors.New("inproc: billing-result: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- a.subscriber.Receive(callCtx, result) }()

	select {
	case err := <-done:
		return err
	case <-callCtx.Done():
		return fmt.Errorf("inproc: billing-result: вызов прерван: %w", callCtx.Err())
	}
}

// Утверждение, что адаптер удовлетворяет владельческому интерфейсу контракта.
var _ billingresult.Publisher = (*Adapter)(nil)
