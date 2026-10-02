// Package inproc — in-process адаптер контракта usage-record.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (usagerecord.Port), с реализацией владельца (usagerecord.Recorder): при
// co-location вызов идёт прямым вызовом в том же процессе, без сети и
// сериализации.
//
// # Семантика
//
// Адаптер обязан сохранять семантику контракта так, чтобы переход к сетевому
// адаптеру не менял наблюдаемое поведение: он соблюдает дедлайн и отмену
// context.Context, проверяет контракт до вызова владельца и дедуплицирует запись
// по ключу идемпотентности (PeriodKey, UsageKey), чтобы повторная доставка не
// расходовала лимит дважды. Ключ записывается только после успеха: неуспешный
// вызов не «съедается», и повтор вправе выполнить запись снова.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"lead-gen/contracts/billing/usagerecord"
)

// defaultTimeout — бюджет вызова по умолчанию, если ни вызывающий, ни сборка не
// задали свой. Ноль означал бы «ждём сколько угодно», а синхронный вызов без
// границы недопустим.
const defaultTimeout = 5 * time.Second

// Option настраивает адаптер при сборке.
type Option func(*Adapter)

// WithTimeout задаёт бюджет вызова. Неположительное значение заменяется
// значением по умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Adapter) { a.timeout = timeout }
}

// Adapter реализует usagerecord.Port поверх реализации владельца. Адаптер
// безопасен для конкурентного использования.
type Adapter struct {
	impl    usagerecord.Recorder
	timeout time.Duration

	mu      sync.Mutex
	applied map[string]struct{}
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
func Bind(impl usagerecord.Recorder, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: usage-record: не задана реализация владельца")
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

// Record реализует usagerecord.Port.
func (a *Adapter) Record(ctx context.Context, record usagerecord.UsageRecord) (usagerecord.Ack, error) {
	if a == nil || a.impl == nil {
		return usagerecord.Ack{}, errors.New("inproc: usage-record: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return usagerecord.Ack{}, err
	}
	if err := record.Validate(); err != nil {
		return usagerecord.Ack{}, err
	}
	if a.appliedBefore(record.IdempotencyKey()) {
		return usagerecord.Ack{Accepted: true, Duplicate: true}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	type outcome struct {
		ack usagerecord.Ack
		err error
	}
	result := make(chan outcome, 1)
	go func() {
		ack, err := a.impl.Record(callCtx, record)
		result <- outcome{ack: ack, err: err}
	}()

	select {
	case out := <-result:
		if out.err != nil {
			return usagerecord.Ack{}, out.err
		}
		a.markApplied(record.IdempotencyKey())
		return out.ack, nil
	case <-callCtx.Done():
		return usagerecord.Ack{}, fmt.Errorf("inproc: usage-record: вызов прерван: %w", callCtx.Err())
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
var _ usagerecord.Port = (*Adapter)(nil)
