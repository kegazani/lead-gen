// Package inproc — in-process адаптер синхронного контракта campaign-command.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (campaigncommand.Port), с реализацией владельца: при co-location вызов идёт
// прямым вызовом в том же процессе, без сети и сериализации.
//
// Адаптер знает конкретный контракт, поэтому его место рядом с контрактом, а не в
// platform/transport/inproc: platform — общий примитив, не знающий контрактов.
//
// Семантика. In-process адаптер сохраняет семантику контракта так, чтобы переход к
// сетевому адаптеру не менял наблюдаемое поведение: принимает context.Context и
// соблюдает дедлайн и отмену (вызов прерывается по отмене контекста или по
// истечении бюджета, даже если реализация владельца ещё не ответила) и не
// подменяет отказ владельца успехом. Идемпотентность обеспечивает сам владелец
// (ключ Submission.IdempotencyKey), поэтому адаптер её не дублирует и повторов не
// «съедает».
package inproc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/contracts/outreach-orchestration/campaigncommand"
)

// defaultTimeout — бюджет вызова по умолчанию, если ни вызывающий, ни сборка не
// задали свой. Синхронный вызов без границы недопустим: бюджет обязан быть у обеих
// сторон связи.
const defaultTimeout = 5 * time.Second

// Option настраивает адаптер при сборке.
type Option func(*Adapter)

// WithTimeout задаёт бюджет вызова. Неположительное значение заменяется
// значением по умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Adapter) { a.timeout = timeout }
}

// Adapter реализует campaigncommand.Port поверх реализации владельца. Адаптер
// безопасен для конкурентного использования.
type Adapter struct {
	impl    campaigncommand.Port
	timeout time.Duration
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
func Bind(impl campaigncommand.Port, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: campaign-command: не задана реализация владельца")
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

// SubmitForApproval реализует campaigncommand.Port.
//
// Порядок шагов: отмена контекста, проверка контракта (без сети), вызов реализации
// владельца с бюджетом. Невалидная команда возвращается локально и не доходит до
// владельца — ошибка видна вызывающему, а не прячется на приёмной стороне.
func (a *Adapter) SubmitForApproval(ctx context.Context, in campaigncommand.Submission) (campaigncommand.Result, error) {
	if a == nil || a.impl == nil {
		return campaigncommand.Result{}, fmt.Errorf("%w: адаптер не собран", campaigncommand.ErrUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return campaigncommand.Result{}, err
	}
	if err := in.Validate(); err != nil {
		return campaigncommand.Result{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// Вызов идёт отдельной горутиной, чтобы отмена и дедлайн соблюдались даже
	// тогда, когда реализация владельца их не проверила: адаптер гарантирует
	// границу вызова так же, как это сделал бы сетевой клиент.
	type outcome struct {
		result campaigncommand.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := a.impl.SubmitForApproval(callCtx, in)
		done <- outcome{result: result, err: err}
	}()

	select {
	case out := <-done:
		return out.result, out.err
	case <-callCtx.Done():
		return campaigncommand.Result{}, fmt.Errorf("%w: %v", campaigncommand.ErrUnavailable, callCtx.Err())
	}
}

// Утверждение, что адаптер удовлетворяет потребительскому интерфейсу контракта.
var _ campaigncommand.Port = (*Adapter)(nil)
