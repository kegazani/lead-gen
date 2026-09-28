package firecrawl

import (
	"context"
	"testing"
	"time"
)

// TestLimiterSerializes проверяет, что лимитер допускает не более одного
// затратного запроса одновременно.
func TestLimiterSerializes(t *testing.T) {
	c := newFakeClock()
	l := NewLimiter(LimiterConfig{Clock: c, Sleep: advancingSleep(c)})

	release1, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("первый Acquire: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, _ := l.Acquire(context.Background())
		close(acquired)
		release2()
	}()

	select {
	case <-acquired:
		t.Fatal("второй Acquire не должен завершиться, пока первый слот занят")
	case <-time.After(50 * time.Millisecond):
	}

	release1()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("второй Acquire должен завершиться после освобождения слота")
	}
}

// TestLimiterMinInterval проверяет выдержку паузы между обычными запросами.
func TestLimiterMinInterval(t *testing.T) {
	c := newFakeClock()
	l := NewLimiter(LimiterConfig{
		MinInterval:   10 * time.Second,
		CrawlInterval: 40 * time.Second,
		Clock:         c,
		Sleep:         advancingSleep(c),
	})

	r1, _ := l.Acquire(context.Background())
	r1()

	start := c.Now()
	r2, _ := l.Acquire(context.Background())
	r2()
	if elapsed := c.Now().Sub(start); elapsed < 10*time.Second {
		t.Fatalf("пауза между обычными запросами = %v, want >= 10s", elapsed)
	}
}

// TestLimiterCrawlInterval проверяет отдельную паузу между запусками crawl.
func TestLimiterCrawlInterval(t *testing.T) {
	c := newFakeClock()
	l := NewLimiter(LimiterConfig{
		MinInterval:   10 * time.Second,
		CrawlInterval: 40 * time.Second,
		Clock:         c,
		Sleep:         advancingSleep(c),
	})

	r1, _ := l.AcquireCrawl(context.Background())
	r1()

	start := c.Now()
	r2, _ := l.AcquireCrawl(context.Background())
	r2()
	if elapsed := c.Now().Sub(start); elapsed < 40*time.Second {
		t.Fatalf("пауза между запусками crawl = %v, want >= 40s", elapsed)
	}
}

// TestLimiterContextCancel проверяет уважение отмены контекста.
func TestLimiterContextCancel(t *testing.T) {
	l := NewLimiter(LimiterConfig{})
	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Acquire(ctx); err == nil {
		t.Fatal("Acquire с отменённым контекстом должен вернуть ошибку")
	}
	release()
}
