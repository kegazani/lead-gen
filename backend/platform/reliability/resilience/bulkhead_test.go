package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBulkheadLimitsConcurrency(t *testing.T) {
	b := NewBulkhead(1)

	if err := b.Acquire(context.Background()); err != nil {
		t.Fatalf("первый Acquire: %v", err)
	}

	// Место занято: второй вызов ждёт и упирается в собственный бюджет.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := b.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("второй Acquire = %v, ожидался отказ по таймауту", err)
	}

	b.Release()
	if err := b.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire после Release: %v", err)
	}
}

func TestBulkheadAcquireRespectsCancellation(t *testing.T) {
	b := NewBulkhead(1)
	if err := b.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire отменённого контекста = %v", err)
	}
}

func TestBulkheadExtraReleaseDoesNotPanic(t *testing.T) {
	b := NewBulkhead(1)
	b.Release() // без Acquire — не должно паниковать и не блокировать
	if err := b.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire после лишнего Release: %v", err)
	}
}
