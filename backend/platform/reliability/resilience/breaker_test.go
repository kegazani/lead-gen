package resilience

import (
	"sync"
	"testing"
	"time"
)

func TestBreakerOpensAfterThreshold(t *testing.T) {
	b := NewBreaker(2, time.Hour)

	if !b.Allow() {
		t.Fatal("замкнутый предохранитель должен пропускать вызовы")
	}
	b.Record(false)
	if !b.Allow() {
		t.Fatal("одного отказа недостаточно для размыкания")
	}
	b.Record(false)
	if b.Allow() {
		t.Fatal("после порога отказов вызовы должны отклоняться")
	}
}

func TestBreakerSuccessResetsFailures(t *testing.T) {
	b := NewBreaker(2, time.Hour)
	b.Record(false)
	b.Record(true) // успех сбрасывает серию
	b.Record(false)
	if !b.Allow() {
		t.Fatal("сброшенная серия не должна размыкать предохранитель")
	}
}

func TestBreakerHalfOpenProbe(t *testing.T) {
	b := NewBreaker(1, time.Millisecond)
	b.Record(false) // размыкание
	if b.Allow() {
		t.Fatal("сразу после размыкания вызовы отклоняются")
	}

	time.Sleep(5 * time.Millisecond)

	if !b.Allow() {
		t.Fatal("после паузы должна пройти пробная попытка")
	}
	if b.Allow() {
		t.Fatal("вторая попытка до завершения пробы не должна проходить")
	}
	b.Record(true) // успешная проба закрывает предохранитель
	if !b.Allow() {
		t.Fatal("успешная проба должна закрыть предохранитель")
	}
}

func TestBreakerFailedProbeReopens(t *testing.T) {
	b := NewBreaker(1, time.Millisecond).(*breaker)
	b.Record(false)
	time.Sleep(5 * time.Millisecond)

	if !b.Allow() {
		t.Fatal("ожидалась пробная попытка")
	}
	b.Record(false) // проба провалилась
	if b.Allow() {
		t.Fatal("провалившаяся проба должна снова разомкнуть предохранитель")
	}
	if b.State() != Open {
		t.Fatalf("состояние = %v, ожидалось Open", b.State())
	}
}

func TestBreakerConcurrent(t *testing.T) {
	b := NewBreaker(100, time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if b.Allow() {
				b.Record(i%2 == 0)
			}
		}(i)
	}
	wg.Wait()
}
