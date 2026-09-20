package resilience

import (
	"sync"
	"time"
)

// Breaker — предохранитель на отдельный downstream. После серии отказов вызовы
// быстро отклоняются, давая сервису деградировать явно, а не ждать таймаут
// каждого вызова (design.md, Решение 8).
type Breaker interface {
	// Allow сообщает, можно ли выполнить вызов. false — downstream считается
	// недоступным, и вызывающий обязан явно сообщить о деградации.
	Allow() bool
	// Record фиксирует исход вызова: успех сбрасывает счётчик отказов,
	// отказ приближает размыкание.
	Record(success bool)
}

// State — состояние предохранителя.
type State int

const (
	// Closed — вызовы разрешены.
	Closed State = iota
	// Open — вызовы отклоняются до истечения паузы.
	Open
	// HalfOpen — разрешена одна пробная попытка.
	HalfOpen
)

// breaker — предохранитель с тремя состояниями.
type breaker struct {
	mu               sync.Mutex
	failureThreshold int
	halfOpenAfter    time.Duration
	failures         int
	state            State
	openedAt         time.Time
	probing          bool
}

// NewBreaker собирает предохранитель: он размыкается после failureThreshold
// подряд отказов и переходит к пробной попытке через halfOpenAfter. Значения
// меньше единицы трактуются как один отказ и нулевая пауза.
func NewBreaker(failureThreshold int, halfOpenAfter time.Duration) Breaker {
	if failureThreshold < 1 {
		failureThreshold = 1
	}
	if halfOpenAfter < 0 {
		halfOpenAfter = 0
	}
	return &breaker{
		failureThreshold: failureThreshold,
		halfOpenAfter:    halfOpenAfter,
		state:            Closed,
	}
}

// Allow реализует Breaker.
func (b *breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Open:
		if time.Since(b.openedAt) >= b.halfOpenAfter {
			// Пауза истекла: пропускаем одну пробную попытку.
			b.state = HalfOpen
			b.probing = true
			return true
		}
		return false
	case HalfOpen:
		// Пока проба не завершилась, остальные вызовы не пропускаем, чтобы
		// не обрушить восстанавливающийся downstream.
		if b.probing {
			return false
		}
		b.probing = true
		return true
	default:
		return true
	}
}

// Record реализует Breaker.
func (b *breaker) Record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case HalfOpen:
		b.probing = false
		if success {
			b.state = Closed
			b.failures = 0
			return
		}
		b.trip()
	case Closed:
		if success {
			b.failures = 0
			return
		}
		b.failures++
		if b.failures >= b.failureThreshold {
			b.trip()
		}
	}
}

// State возвращает текущее состояние; нужно тестам и диагностике.
func (b *breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// trip размыкает предохранитель и запоминает момент размыкания.
func (b *breaker) trip() {
	b.state = Open
	b.openedAt = time.Now()
	b.probing = false
}
