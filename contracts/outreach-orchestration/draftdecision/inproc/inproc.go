// Package inproc — in-process адаптер read-контракта draftdecision.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (draftdecision.Source), с реализацией владельца: при co-location вызов идёт
// прямым вызовом в том же процессе, без сети и сериализации.
//
// Адаптер знает конкретный контракт, поэтому его место рядом с контрактом, а не в
// platform/transport/inproc.
//
// Семантика. In-process адаптер сохраняет семантику контракта так, чтобы переход к
// сетевому адаптеру не менял наблюдаемое поведение: принимает context.Context и
// соблюдает дедлайн и отмену и не подменяет недоступность выдуманным решением.
// Исходы чтения (ErrNotFound/ErrPending/ErrRejected) пробрасываются как есть.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
)

// defaultTimeout — бюджет вызова по умолчанию, если ни вызывающий, ни сборка не
// задали свой.
const defaultTimeout = 5 * time.Second

// Option настраивает адаптер при сборке.
type Option func(*Adapter)

// WithTimeout задаёт бюджет вызова. Неположительное значение заменяется
// значением по умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Adapter) { a.timeout = timeout }
}

// Adapter реализует draftdecision.Source поверх реализации владельца. Адаптер
// безопасен для конкурентного использования.
type Adapter struct {
	impl    draftdecision.Source
	timeout time.Duration
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
func Bind(impl draftdecision.Source, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: draftdecision: не задана реализация владельца")
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

// ApprovedAction реализует draftdecision.Source. Недоступность владельца
// переводится в ErrUnavailable; исходы чтения владельца пробрасываются как есть.
func (a *Adapter) ApprovedAction(ctx context.Context, in draftdecision.Lookup) (approvedaction.ApprovedAction, error) {
	if a == nil || a.impl == nil {
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: адаптер не собран", draftdecision.ErrUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return approvedaction.ApprovedAction{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	type outcome struct {
		action approvedaction.ApprovedAction
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		action, err := a.impl.ApprovedAction(callCtx, in)
		done <- outcome{action: action, err: err}
	}()

	select {
	case out := <-done:
		return out.action, out.err
	case <-callCtx.Done():
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %v", draftdecision.ErrUnavailable, callCtx.Err())
	}
}

// Утверждение, что адаптер удовлетворяет потребительскому интерфейсу контракта.
var _ draftdecision.Source = (*Adapter)(nil)
