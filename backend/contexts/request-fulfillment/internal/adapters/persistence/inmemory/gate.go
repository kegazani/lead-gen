package inmemory

import (
	"context"
	"errors"
)

// GateHooks наблюдает contention/acquire в deterministic tests. Production
// constructor использует nil hooks; callbacks не являются глобальными seams.
type GateHooks struct {
	Contended func()
	Acquired  func()
}

// Gate — эксклюзивный context-aware semaphore для всех операций над одним
// memory repository и связанным outbox journal.
type Gate struct {
	token chan struct{}
	hooks *GateHooks
}

func NewGate() *Gate { return NewGateWithHooks(nil) }

// NewGateWithHooks нужен адаптерным тестам, чтобы синхронизировать реальное
// ожидание и захват без scheduling delays. Не используйте hooks в production.
func NewGateWithHooks(hooks *GateHooks) *Gate {
	g := &Gate{token: make(chan struct{}, 1), hooks: hooks}
	g.token <- struct{}{}
	return g
}

// Acquire ждёт gate с отменой context. Проверка после получения permit закрывает
// гонку, когда отмена и свободный gate становятся готовы одновременно.
func (g *Gate) Acquire(ctx context.Context) error {
	if ctx == nil {
		return errors.New("inmemory: nil gate context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	acquired := false
	select {
	case <-g.token:
		acquired = true
	default:
		if g.hooks != nil && g.hooks.Contended != nil {
			g.hooks.Contended()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-g.token:
			acquired = true
		}
	}
	if !acquired {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		g.Release()
		return err
	}
	if g.hooks != nil && g.hooks.Acquired != nil {
		g.hooks.Acquired()
	}
	if err := ctx.Err(); err != nil {
		g.Release()
		return err
	}
	return nil
}

// Release публикует permit ожидающему участнику. Вызывающий обязан вызывать его
// ровно один раз после успешного Acquire, в том числе при panic callback-а.
func (g *Gate) Release() { g.token <- struct{}{} }
