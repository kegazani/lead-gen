package observ

import (
	"sync"
	"testing"
)

func TestMemoryMetricsCounter(t *testing.T) {
	m := NewMetrics().(*MemoryMetrics)
	m.Incr("http_requests_total", map[string]string{"service": "outreach-orchestration", "code": "200"})
	m.Incr("http_requests_total", map[string]string{"code": "200", "service": "outreach-orchestration"})

	// Порядок меток не должен влиять на ключ образца.
	if got := m.Counter("http_requests_total", map[string]string{"service": "outreach-orchestration", "code": "200"}); got != 2 {
		t.Fatalf("счётчик = %d, ожидалось 2", got)
	}
	if got := m.Counter("http_requests_total", map[string]string{"code": "500", "service": "outreach-orchestration"}); got != 0 {
		t.Fatalf("чужой образец должен быть пуст: %d", got)
	}
}

func TestMemoryMetricsObservation(t *testing.T) {
	m := NewMetrics().(*MemoryMetrics)
	m.Observe("http_duration_seconds", 0.1, map[string]string{"service": "s"})
	m.Observe("http_duration_seconds", 0.3, map[string]string{"service": "s"})

	sum, count := m.Observation("http_duration_seconds", map[string]string{"service": "s"})
	if count != 2 {
		t.Fatalf("count = %d, ожидалось 2", count)
	}
	if sum < 0.39 || sum > 0.41 {
		t.Fatalf("sum = %v, ожидалось ~0.4", sum)
	}
}

func TestMemoryMetricsConcurrent(t *testing.T) {
	m := NewMetrics().(*MemoryMetrics)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Incr("calls_total", map[string]string{"service": "s"})
			m.Observe("latency", 1, map[string]string{"service": "s"})
		}()
	}
	wg.Wait()

	if got := m.Counter("calls_total", map[string]string{"service": "s"}); got != 50 {
		t.Fatalf("счётчик = %d, ожидалось 50", got)
	}
	if _, count := m.Observation("latency", map[string]string{"service": "s"}); count != 50 {
		t.Fatalf("наблюдений = %d, ожидалось 50", count)
	}
}
