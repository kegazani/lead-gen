// Package inproc — in-process адаптер контракта billing-entitlement.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (billingentitlement.Port), с реализацией владельца
// (billingentitlement.Source): при co-location вызов идёт прямым вызовом в том же
// процессе.
//
// # Семантика
//
// Адаптер сохраняет семантику контракта так, чтобы переход к сетевому адаптеру
// не менял наблюдаемое поведение: соблюдает дедлайн и отмену context.Context,
// проверяет прочитанные права и не подставляет права по умолчанию при отказе.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/contracts/billing/billingentitlement"
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

// Adapter реализует billingentitlement.Port поверх реализации владельца.
type Adapter struct {
	impl    billingentitlement.Source
	timeout time.Duration
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
func Bind(impl billingentitlement.Source, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: billing-entitlement: не задана реализация владельца")
	}
	adapter := &Adapter{impl: impl, timeout: defaultTimeout}
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

// Entitlement реализует billingentitlement.Port.
func (a *Adapter) Entitlement(ctx context.Context, tenantID string) (billingentitlement.Entitlement, error) {
	if a == nil || a.impl == nil {
		return billingentitlement.Entitlement{}, errors.New("inproc: billing-entitlement: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return billingentitlement.Entitlement{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	type outcome struct {
		entitlement billingentitlement.Entitlement
		err         error
	}
	result := make(chan outcome, 1)
	go func() {
		entitlement, err := a.impl.Entitlement(callCtx, tenantID)
		result <- outcome{entitlement: entitlement, err: err}
	}()

	select {
	case out := <-result:
		if out.err != nil {
			return billingentitlement.Entitlement{}, out.err
		}
		if err := out.entitlement.Validate(); err != nil {
			return billingentitlement.Entitlement{}, err
		}
		return out.entitlement, nil
	case <-callCtx.Done():
		return billingentitlement.Entitlement{}, fmt.Errorf("inproc: billing-entitlement: вызов прерван: %w", callCtx.Err())
	}
}

// Утверждение, что адаптер удовлетворяет потребительскому интерфейсу контракта.
var _ billingentitlement.Port = (*Adapter)(nil)
