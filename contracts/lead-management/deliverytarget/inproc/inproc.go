// Package inproc — in-process адаптер синхронного контракта delivery-target.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (deliverytarget.Resolver), с реализацией владельца: при co-location вызов идёт
// прямым вызовом в том же процессе, без сети и сериализации.
//
// # Почему адаптер лежит рядом с контрактом
//
// Адаптер знает конкретный контракт — форму deliverytarget.Resolver, — поэтому
// его место рядом с контрактом, а не в platform/transport. platform — общий
// примитив, не знающий контрактов: положить туда адаптер значило бы заставить
// platform зависеть от published language конкретного владельца.
//
// # Семантика
//
// In-process адаптер сохраняет семантику контракта так, чтобы переход к сетевому
// адаптеру не менял наблюдаемое поведение: принимает context.Context и соблюдает
// дедлайн и отмену, отвергает пустые входы до вызова владельца и пропускает
// доменные исходы (ErrRejected/ErrUnavailable) без подмены.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/contracts/lead-management/deliverytarget"
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

// Adapter реализует deliverytarget.Resolver поверх реализации владельца. Адаптер
// безопасен для конкурентного использования (состояния не хранит).
type Adapter struct {
	impl    deliverytarget.Resolver
	timeout time.Duration
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
// Возвращённое значение — то, что видит потребитель; оно не даёт доступа к
// реализации владельца, кроме вызова контракта.
func Bind(impl deliverytarget.Resolver, options ...Option) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: delivery-target: не задана реализация владельца")
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

// ResolveRecipient реализует deliverytarget.Resolver.
//
// Порядок шагов: отмена контекста, проверка входов, затем вызов реализации
// владельца с бюджетом. Пустые тенант или ссылка отвергаются как доменный отказ до
// внешнего вызова; отмена и дедлайн соблюдаются даже тогда, когда реализация
// владельца их не проверила.
func (a *Adapter) ResolveRecipient(ctx context.Context, tenantID, recipientRef string) (deliverytarget.Recipient, error) {
	if a == nil || a.impl == nil {
		return deliverytarget.Recipient{}, errors.New("inproc: delivery-target: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return deliverytarget.Recipient{}, err
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(recipientRef) == "" {
		return deliverytarget.Recipient{}, fmt.Errorf("%w: tenant_id и recipient_ref обязательны", deliverytarget.ErrRejected)
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// Вызов идёт отдельной горутиной, чтобы отмена и дедлайн соблюдались даже
	// тогда, когда реализация владельца их не проверила: адаптер гарантирует
	// границу вызова так же, как это сделал бы сетевой клиент.
	type outcome struct {
		recipient deliverytarget.Recipient
		err       error
	}
	result := make(chan outcome, 1)
	go func() {
		recipient, err := a.impl.ResolveRecipient(callCtx, tenantID, recipientRef)
		result <- outcome{recipient: recipient, err: err}
	}()

	select {
	case out := <-result:
		return out.recipient, out.err
	case <-callCtx.Done():
		return deliverytarget.Recipient{}, fmt.Errorf("inproc: delivery-target: вызов прерван: %w", callCtx.Err())
	}
}

// Утверждение, что адаптер удовлетворяет потребительскому интерфейсу контракта.
var _ deliverytarget.Resolver = (*Adapter)(nil)
