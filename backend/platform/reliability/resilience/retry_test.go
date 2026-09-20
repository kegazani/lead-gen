package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryPolicyRepeatsIdempotentUntilSuccess(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
	calls := 0
	err := policy.Do(context.Background(), true, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("временный отказ")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 3 {
		t.Fatalf("попыток = %d, ожидалось 3", calls)
	}
}

func TestRetryPolicyDoesNotRepeatNonIdempotent(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}
	calls := 0
	want := errors.New("отказ")
	err := policy.Do(context.Background(), false, func(context.Context) error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("ошибка = %v, ожидалась %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("неидемпотентная операция повторена %d раз", calls)
	}
}

func TestRetryPolicyStopsAfterMaxAttempts(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond}
	calls := 0
	want := errors.New("отказ")
	err := policy.Do(context.Background(), true, func(context.Context) error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("ошибка = %v, ожидалась %v", err, want)
	}
	if calls != 3 {
		t.Fatalf("попыток = %d, ожидалось 3", calls)
	}
}

func TestRetryPolicyRespectsContextCancellation(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 10, BaseDelay: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := policy.Do(ctx, true, func(context.Context) error {
		calls++
		cancel() // отменяем бюджет сразу после первой неудачи
		return errors.New("отказ")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v, ожидалось context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("после отмены попыток больше не должно быть: %d", calls)
	}
}

func TestRetryPolicyMinAttemptsIsOne(t *testing.T) {
	calls := 0
	err := (RetryPolicy{}).Do(context.Background(), true, func(context.Context) error {
		calls++
		return errors.New("отказ")
	})
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if calls != 1 {
		t.Fatalf("попыток = %d, ожидалось 1", calls)
	}
}

func TestRetryPolicyDelayGrowsAndCaps(t *testing.T) {
	p := RetryPolicy{BaseDelay: 10 * time.Millisecond, MaxDelay: 25 * time.Millisecond}
	if got := p.delay(0); got != 10*time.Millisecond {
		t.Fatalf("delay(0) = %v", got)
	}
	if got := p.delay(1); got != 20*time.Millisecond {
		t.Fatalf("delay(1) = %v", got)
	}
	if got := p.delay(2); got != 25*time.Millisecond {
		t.Fatalf("delay(2) = %v, ожидалось ограничение MaxDelay", got)
	}
	if got := p.delay(10); got != 25*time.Millisecond {
		t.Fatalf("delay(10) = %v, ожидалось ограничение MaxDelay", got)
	}
}

func TestRetryPolicyJitterStaysWithinDelay(t *testing.T) {
	p := RetryPolicy{BaseDelay: 20 * time.Millisecond, Jitter: true}
	for i := 0; i < 20; i++ {
		if got := p.delay(0); got < 0 || got > 20*time.Millisecond {
			t.Fatalf("джиттер вышел за границы: %v", got)
		}
	}
}
