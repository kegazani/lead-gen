// Package inproc — in-process адаптер синхронного контракта access-decision.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (accessdecision.Decider), с реализацией владельца: при co-location вызов идёт
// прямым вызовом в том же процессе, без сети и сериализации.
//
// # Почему адаптер лежит рядом с контрактом
//
// Адаптер знает конкретный контракт — интерфейс accessdecision.Decider, — поэтому
// его место рядом с контрактом, а не в platform/transport/inproc. platform —
// общий примитив, не знающий контрактов.
//
// # Семантика
//
// In-process адаптер обязан сохранять семантику контракта так, чтобы переход к
// сетевому адаптеру не менял наблюдаемое поведение (design.md, Решение 9):
//
//   - принимает context.Context и соблюдает дедлайн и отмену: вызов прерывается
//     по отмене контекста или по истечении бюджета, даже если реализация
//     владельца ещё не ответила;
//   - недоступность владельца — честный отказ (ошибка), а не «разрешено».
//
// Адаптер не подменяет сетевой транспорт: он только выравнивает наблюдаемое
// поведение при co-location.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/contracts/platform-governance/accessdecision"
)

// defaultTimeout — бюджет вызова по умолчанию, если сборка не задала свой. Ноль
// означал бы «ждём сколько угодно», а синхронный вызов без границы недопустим.
const defaultTimeout = 5 * time.Second

// Option настраивает адаптер при сборке.
type Option func(*Adapter)

// WithTimeout задаёт бюджет вызова. Неположительное значение заменяется
// значением по умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Adapter) { a.timeout = timeout }
}

// Adapter реализует accessdecision.Decider поверх реализации владельца.
type Adapter struct {
	impl    accessdecision.Decider
	timeout time.Duration
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
// Возвращённое значение — то, что видит потребитель; оно не даёт доступа к
// реализации владельца, кроме вызова контракта. nil-реализация отвергается
// ошибкой сборки: молчаливое «разрешено» недопустимо.
func Bind(impl accessdecision.Decider, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: access-decision: не задана реализация владельца")
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

// CanAct реализует accessdecision.Decider.
//
// Вызов идёт отдельной горутиной, чтобы отмена и дедлайн соблюдались даже тогда,
// когда реализация владельца их не проверила. Ошибка не превращается в
// положительное решение.
func (a *Adapter) CanAct(ctx context.Context, actor, tenant string) (bool, error) {
	if a == nil || a.impl == nil {
		return false, accessdecision.ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	type result struct {
		allowed bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		allowed, err := a.impl.CanAct(callCtx, actor, tenant)
		done <- result{allowed: allowed, err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return false, r.err
		}
		return r.allowed, nil
	case <-callCtx.Done():
		return false, fmt.Errorf("inproc: access-decision: вызов прерван: %w", callCtx.Err())
	}
}

// Проверка соответствия контракту на этапе компиляции.
var _ accessdecision.Decider = (*Adapter)(nil)
