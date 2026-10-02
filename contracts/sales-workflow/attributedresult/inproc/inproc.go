// Package inproc — in-process адаптер синхронной публикации контракта
// attributed-result.
//
// Адаптер связывает потребителя, держащего интерфейс контракта
// (attributedresult.Publisher), с реализацией владельца: при co-location вызов
// идёт прямым вызовом в том же процессе, без сети и сериализации. Пакет лежит
// рядом с контрактом и зависит только от стандартной библиотеки и самого
// контракта.
//
// # Семантика
//
// Адаптер сохраняет семантику контракта так, чтобы переход к сетевому адаптеру
// не менял наблюдаемое поведение:
//
//   - принимает context.Context и соблюдает отмену;
//   - пробрасывает и соблюдает ключ идемпотентности (tenant_id, external_ref):
//     повторная доставка того же результата применяется один раз;
//   - отвергает невалидный контракт, не доходя до реализации владельца.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"lead-gen/contracts/sales-workflow/attributedresult"
)

// Adapter реализует attributedresult.Publisher поверх реализации владельца.
// Адаптер безопасен для конкурентного использования.
type Adapter struct {
	impl attributedresult.Publisher

	mu      sync.Mutex
	applied map[string]struct{}
}

// Bind связывает реализацию владельца с потребительским интерфейсом контракта.
func Bind(impl attributedresult.Publisher) (*Adapter, error) {
	if impl == nil {
		return nil, errors.New("inproc: attributed-result: не задана реализация владельца")
	}
	return &Adapter{impl: impl, applied: make(map[string]struct{})}, nil
}

// Publish реализует attributedresult.Publisher.
//
// Порядок шагов: отмена контекста, проверка контракта, дедупликация по ключу
// идемпотентности, затем вызов реализации владельца. Повторная доставка уже
// применённого ключа не вызывает владельца повторно; ключ фиксируется только
// после успеха, поэтому неуспешный вызов можно повторить.
func (a *Adapter) Publish(ctx context.Context, result attributedresult.AttributedResult) error {
	if a == nil || a.impl == nil {
		return errors.New("inproc: attributed-result: адаптер не собран")
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
	key := result.IdempotencyKey()
	if a.appliedBefore(key) {
		return nil
	}
	if err := a.impl.Publish(ctx, result); err != nil {
		return fmt.Errorf("inproc: attributed-result: %w", err)
	}
	a.markApplied(key)
	return nil
}

func (a *Adapter) appliedBefore(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.applied[key]
	return ok
}

func (a *Adapter) markApplied(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applied[key] = struct{}{}
}

// Утверждение, что адаптер удовлетворяет потребительскому интерфейсу контракта.
var _ attributedresult.Publisher = (*Adapter)(nil)
