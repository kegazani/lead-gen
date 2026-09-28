package firecrawl

import (
	"context"
	"sync"
	"time"
)

// fakeClock — фейковые часы с ручным продвижением для детерминированных тестов.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// advancingSleep — задержка, мгновенно продвигающая фейковые часы на d. Симулирует
// реальное течение времени без реального ожидания.
func advancingSleep(c *fakeClock) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		c.Advance(d)
		return nil
	}
}

// recordedSleep — записывает задержки без реального ожидания.
type recordedSleep struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (r *recordedSleep) fn() func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		r.mu.Lock()
		r.delays = append(r.delays, d)
		r.mu.Unlock()
		return nil
	}
}

func (r *recordedSleep) list() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.delays...)
}
