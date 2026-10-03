package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubHealth — управляемый источник проверки доступности: тест задаёт исход
// каждой проверки.
type stubHealth struct{ err error }

// CheckHealth реализует HealthChecker.
func (s *stubHealth) CheckHealth(context.Context) error { return s.err }

// TestAvailabilityMonitorProbeToggles проверяет, что монитор переключает признак
// готовности по исходу проверки: сбой — неготовность, восстановление — снова
// готовность.
func TestAvailabilityMonitorProbeToggles(t *testing.T) {
	availability := NewAvailability()
	availability.MarkReady()
	checker := &stubHealth{}
	monitor := &AvailabilityMonitor{Checker: checker, Availability: availability}

	monitored := true
	monitor.probe(context.Background(), time.Second, &monitored)
	if !availability.Ready() || !monitored {
		t.Fatal("успешная проверка не должна снимать готовность")
	}

	checker.err = errors.New("рантайм недоступен")
	monitor.probe(context.Background(), time.Second, &monitored)
	if availability.Ready() || monitored {
		t.Fatal("сбой проверки должен снимать готовность")
	}

	checker.err = nil
	monitor.probe(context.Background(), time.Second, &monitored)
	if !availability.Ready() || !monitored {
		t.Fatal("восстановление рантайма должно возвращать готовность")
	}
}

// TestAvailabilityMonitorRunStopsWithContext проверяет, что монитор выполняет
// проверку сразу при старте и завершается по отмене контекста, не дожидаясь тика.
func TestAvailabilityMonitorRunStopsWithContext(t *testing.T) {
	availability := NewAvailability()
	availability.MarkReady()
	monitor := &AvailabilityMonitor{
		Checker:      &stubHealth{err: errors.New("нет связи")},
		Availability: availability,
		Interval:     time.Hour,
		Timeout:      time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- monitor.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("монитор завершился ошибкой: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("монитор не завершился по отмене контекста")
	}
	if availability.Ready() {
		t.Fatal("стартовая проверка должна была снять готовность")
	}
}

// TestAvailabilityMonitorNilIsNoOp проверяет, что монитор без цели безопасен.
func TestAvailabilityMonitorNilIsNoOp(t *testing.T) {
	var monitor *AvailabilityMonitor
	if err := monitor.Run(context.Background()); err != nil {
		t.Fatalf("nil-монитор вернул ошибку: %v", err)
	}
	empty := &AvailabilityMonitor{}
	if err := empty.Run(context.Background()); err != nil {
		t.Fatalf("пустой монитор вернул ошибку: %v", err)
	}
}
