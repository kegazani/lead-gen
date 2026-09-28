// Package inproc — in-process адаптер синхронного контракта provider-port.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (providerport.Port), с реализацией владельца: при co-location вызов идёт
// прямым вызовом в том же процессе, без сети и сериализации.
//
// # Почему адаптер лежит рядом с контрактом
//
// Адаптер знает конкретный контракт — тип providerport.Request и его ключ
// идемпотентности, — поэтому его место рядом с контрактом, а не в
// platform/transport/inproc. platform — общий примитив, не знающий контрактов:
// положить туда адаптер значило бы заставить platform зависеть от published
// language конкретного владельца. Рядом с контрактом пакет зависит только от
// стандартной библиотеки и самого контракта.
//
// # Семантика
//
// In-process адаптер обязан сохранять семантику контракта так, чтобы переход к
// сетевому адаптеру не менял наблюдаемое поведение (design.md, Решение 8):
//
//   - принимает context.Context и соблюдает дедлайн и отмену: вызов прерывается
//     по отмене контекста или по истечении бюджета, даже если реализация
//     владельца ещё не ответила. Иначе сетевая версия внезапно добавила бы
//     таймауты и частичный отказ, которых in-process не знал;
//   - проверяет контракт до вызова: невалидный запрос не доходит до владельца;
//   - соблюдает ключ идемпотентности: повторная доставка с тем же ключом не
//     вызывает владельца повторно, а возвращает сохранённый результат прежнего
//     вызова — повтор не множит внешний эффект.
//
// Адаптер не подменяет сетевой транспорт: он только выравнивает наблюдаемое
// поведение при co-location. Сетевой http-json-клиент появится рядом, в точке
// сборки сервиса, при разделении сервисов по процессам.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"lead-gen/contracts/integration-platform/providerport"
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

// Adapter реализует providerport.Port поверх реализации владельца. Адаптер
// безопасен для конкурентного использования.
type Adapter struct {
	impl    providerport.Port
	timeout time.Duration

	mu      sync.Mutex
	applied map[string]providerport.Result
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
// Возвращённое значение — то, что видит потребитель; оно не даёт доступа к
// реализации владельца, кроме вызова контракта.
func Bind(impl providerport.Port, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: provider-port: не задана реализация владельца")
	}
	adapter := &Adapter{
		impl:    impl,
		timeout: defaultTimeout,
		applied: make(map[string]providerport.Result),
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

// Invoke реализует providerport.Port.
//
// Порядок шагов: отмена контекста, проверка контракта, дедупликация по ключу
// идемпотентности, затем вызов реализации владельца с бюджетом. Повторная
// доставка уже применённого ключа не вызывает владельца повторно и возвращает
// сохранённый результат. Ключ записывается только после успеха: неуспешный
// вызов не «съедается», и повтор вправе выполнить операцию снова.
func (a *Adapter) Invoke(ctx context.Context, request providerport.Request) (providerport.Result, error) {
	if a == nil || a.impl == nil {
		return providerport.Result{}, errors.New("inproc: provider-port: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return providerport.Result{}, err
	}
	if err := request.Validate(); err != nil {
		return providerport.Result{}, err
	}

	key := request.IdempotencyKey()
	if result, ok := a.appliedBefore(key); ok {
		return result, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// Вызов идёт отдельной горутиной, чтобы отмена и дедлайн соблюдались даже
	// тогда, когда реализация владельца их не проверила: адаптер гарантирует
	// границу вызова так же, как это сделал бы сетевой клиент.
	type outcome struct {
		result providerport.Result
		err    error
	}
	result := make(chan outcome, 1)
	go func() {
		callResult, err := a.impl.Invoke(callCtx, request)
		result <- outcome{result: callResult, err: err}
	}()

	select {
	case out := <-result:
		if out.err != nil {
			return providerport.Result{}, out.err
		}
		a.markApplied(key, out.result)
		return out.result, nil
	case <-callCtx.Done():
		return providerport.Result{}, fmt.Errorf("inproc: provider-port: вызов прерван: %w", callCtx.Err())
	}
}

// appliedBefore возвращает сохранённый результат, если ключ применялся ранее.
func (a *Adapter) appliedBefore(key string) (providerport.Result, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result, ok := a.applied[key]
	return result, ok
}

// markApplied фиксирует применённый ключ вместе с результатом вызова.
func (a *Adapter) markApplied(key string, result providerport.Result) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applied[key] = result
}

// Утверждение, что адаптер удовлетворяет потребительскому интерфейсу контракта.
var _ providerport.Port = (*Adapter)(nil)
