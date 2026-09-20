package resilience

import (
	"context"
	"math/rand"
	"time"
)

// RetryPolicy описывает повтор идемпотентных операций: конечное число попыток,
// экспоненциальную задержку с ограничением и необязательный джиттер.
type RetryPolicy struct {
	// MaxAttempts — общее число попыток, включая первую. Значение меньше
	// единицы трактуется как одна попытка.
	MaxAttempts int
	// BaseDelay — задержка перед первым повтором. Ноль отключает паузу.
	BaseDelay time.Duration
	// MaxDelay — верхняя граница задержки. Ноль означает отсутствие границы.
	MaxDelay time.Duration
	// Jitter добавляет к задержке случайный разброс, чтобы повторы разных
	// вызовов не совпадали по времени и не били по downstream залпом.
	Jitter bool
}

// Do выполняет fn и повторяет её только при idempotent == true. Неидемпотентная
// операция выполняется ровно один раз: автоматический повтор мог бы создать
// второй эффект.
//
// Повтор идёт с экспоненциальной задержкой (BaseDelay, удвоение до MaxDelay) и
// конечным числом попыток. Отмена контекста прерывает паузу и возвращает
// context.Canceled/DeadlineExceeded, а не последнюю ошибку операции: вызывающий
// должен различать отказ downstream и исчерпание собственного бюджета.
func (p RetryPolicy) Do(ctx context.Context, idempotent bool, fn func(context.Context) error) error {
	attempts := p.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}
		if !idempotent || attempt == attempts-1 {
			break
		}
		if err := sleep(ctx, p.delay(attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

// delay возвращает задержку перед повтором номер attempt (0 — первый повтор).
func (p RetryPolicy) delay(attempt int) time.Duration {
	d := p.BaseDelay
	if d <= 0 {
		return 0
	}
	for i := 0; i < attempt; i++ {
		if p.MaxDelay > 0 && d >= p.MaxDelay {
			break
		}
		d *= 2
	}
	if p.MaxDelay > 0 && d > p.MaxDelay {
		d = p.MaxDelay
	}
	if !p.Jitter {
		return d
	}
	// Полный джиттер: задержка равномерна в [0, d]. Границы сохраняют
	// ограничение сверху, а нижняя нулевая граница даёт максимальный разброс.
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d) + 1))
}

// sleep ждёт d, прерываясь по отмене контекста. Нулевая задержка не блокирует.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
