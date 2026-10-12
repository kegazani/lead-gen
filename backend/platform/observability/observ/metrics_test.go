package observ

import (
	"math"
	"strings"
	"sync"
	"testing"

	"lead-gen/backend/platform/observability/metriccatalog"
)

type metricSample struct {
	name   string
	kind   metriccatalog.Kind
	labels map[string]string
}

// TestValidateLabelsCardinalityGuard проверяет сам примитив валидации
// (design.md, Решение 6): допустимы только имена из белого списка, значения в
// пределах длины и перечислимые значения; высококардинальные идентификаторы
// (user_id/workflow_id), URL и произвольный текст отбрасываются. Прямой тест
// ValidateLabels дополняет проверку через MemoryMetrics: адаптер prommetrics
// применяет ту же функцию, поэтому её поведение — общий контракт.
func TestValidateLabelsCardinalityGuard(t *testing.T) {
	catalog := metriccatalog.Default()
	valid := []metricSample{
		{name: "process_self_terminated_total", kind: metriccatalog.Counter},
		{name: "http_duration_seconds", kind: metriccatalog.Histogram, labels: map[string]string{"service": "outreach-orchestration"}},
		{name: "http_requests_total", kind: metriccatalog.Counter, labels: map[string]string{"service": "lead-management", "code": "200"}},
		{name: "jobs_total", kind: metriccatalog.Counter, labels: map[string]string{"service": "analytics", "outcome": "done"}},
		{name: "port_invocations_total", kind: metriccatalog.Counter, labels: map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}},
	}
	for _, s := range valid {
		if !catalog.Validate(s.name, s.kind, s.labels) {
			t.Errorf("допустимая схема %s с метками %v отвергнута", s.name, s.labels)
		}
	}

	tooLong := strings.Repeat("a", 129)
	invalid := []struct {
		name   string
		labels map[string]string
	}{
		{"идентификатор сущности user_id", map[string]string{"service": "analytics", "user_id": "u-1"}},
		{"идентификатор сущности workflow_id", map[string]string{"service": "analytics", "workflow_id": "wf-1"}},
		{"URL в значении", map[string]string{"service": "https://example.com"}},
		{"значение сверх лимита длины", map[string]string{"service": tooLong}},
		{"пустое значение", map[string]string{"service": ""}},
		{"неизвестный service", map[string]string{"service": "unknown-service"}},
		{"недопустимый исход", map[string]string{"service": "analytics", "outcome": "произвольный-текст"}},
		{"нечисловой код статуса", map[string]string{"service": "analytics", "code": "abc"}},
		{"внедиапазонный код статуса", map[string]string{"service": "analytics", "code": "999"}},
	}
	for _, c := range invalid {
		if catalog.Validate("http_requests_total", metriccatalog.Counter, c.labels) {
			t.Errorf("%s: метки %v ошибочно признаны допустимыми", c.name, c.labels)
		}
	}
}

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
	m.Observe("http_duration_seconds", 0.1, map[string]string{"service": "analytics"})
	m.Observe("http_duration_seconds", 0.3, map[string]string{"service": "analytics"})

	sum, count := m.Observation("http_duration_seconds", map[string]string{"service": "analytics"})
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
			m.Incr("jobs_total", map[string]string{"service": "analytics", "outcome": "done"})
			m.Observe("jobs_duration_seconds", 1, map[string]string{"service": "analytics"})
		}()
	}
	wg.Wait()

	if got := m.Counter("jobs_total", map[string]string{"service": "analytics", "outcome": "done"}); got != 50 {
		t.Fatalf("счётчик = %d, ожидалось 50", got)
	}
	if _, count := m.Observation("jobs_duration_seconds", map[string]string{"service": "analytics"}); count != 50 {
		t.Fatalf("наблюдений = %d, ожидалось 50", count)
	}
}

func TestMemoryMetricsRejectsInvalidLabels(t *testing.T) {
	m := NewMetrics().(*MemoryMetrics)

	// Допустимые метки проходят: service, код статуса и исход задания.
	m.Incr("http_requests_total", map[string]string{"service": "outreach-orchestration", "code": "200"})
	m.Incr("jobs_total", map[string]string{"service": "analytics", "outcome": "done"})
	if got := m.Counter("http_requests_total", map[string]string{"service": "outreach-orchestration", "code": "200"}); got != 1 {
		t.Fatalf("счётчик допустимого образца = %d, ожидалось 1", got)
	}

	// Имя метки вне белого списка (идентификатор сущности) — образец не учитывается.
	m.Incr("http_requests_total", map[string]string{"service": "analytics", "workflow_id": "wf-1"})
	if got := m.Counter("http_requests_total", map[string]string{"service": "analytics", "workflow_id": "wf-1"}); got != 0 {
		t.Fatalf("образец с идентификатором учтён: %d", got)
	}

	// Значение-URL — высококардинальная подмена — не создаёт серию.
	m.Incr("downstream_errors_total", map[string]string{"service": "https://example.com", "engine": "llm"})
	if got := m.Counter("downstream_errors_total", map[string]string{"service": "https://example.com", "engine": "llm"}); got != 0 {
		t.Fatalf("образец с URL учтён: %d", got)
	}

	// Значение исхода вне перечисления — не создаёт серию.
	m.Incr("jobs_total", map[string]string{"service": "analytics", "outcome": "произвольный-текст"})
	if got := m.Counter("jobs_total", map[string]string{"service": "analytics", "outcome": "произвольный-текст"}); got != 0 {
		t.Fatalf("образец с недопустимым исходом учтён: %d", got)
	}
}

func TestClosedLabelPolicyRejectsShortPIIForEveryLabel(t *testing.T) {
	policy := metriccatalog.Default()
	checks := map[string]metricSample{
		"service":   {name: "http_duration_seconds", kind: metriccatalog.Histogram, labels: map[string]string{"service": "analytics"}},
		"role":      {name: "role_exit_live_total", kind: metriccatalog.Counter, labels: map[string]string{"service": "analytics", "role": "worker"}},
		"port":      {name: "port_invocations_total", kind: metriccatalog.Counter, labels: map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}},
		"adapter":   {name: "port_invocations_total", kind: metriccatalog.Counter, labels: map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}},
		"operation": {name: "port_invocations_total", kind: metriccatalog.Counter, labels: map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}},
		"engine":    {name: "downstream_errors_total", kind: metriccatalog.Counter, labels: map[string]string{"service": "lead-management", "engine": "search"}},
	}
	for label, valid := range checks {
		for _, value := range []string{"a@b.co", "550e8400-e29b-41d4-a716-446655440000", "tok_abc123", "unregistered"} {
			labels := cloneMetricLabels(valid.labels)
			labels[label] = value
			if policy.Validate(valid.name, valid.kind, labels) {
				t.Errorf("метка %s допустила неизвестное/чувствительное значение %q", label, value)
			}
		}
		if !policy.Validate(valid.name, valid.kind, valid.labels) {
			t.Errorf("известное значение %s отвергнуто: %+v", label, valid.labels)
		}
	}
}

func cloneMetricLabels(labels map[string]string) map[string]string {
	copy := make(map[string]string, len(labels))
	for name, value := range labels {
		copy[name] = value
	}
	return copy
}

func TestMemoryMetricsRejectsUnknownWithoutGrowingSeries(t *testing.T) {
	metrics := NewMetricsWithCatalog(metriccatalog.Default())
	metrics.Incr("unregistered_metric", map[string]string{"service": "analytics"})
	metrics.Incr("http_requests_total", map[string]string{"service": "analytics"})
	metrics.Incr("http_requests_total", map[string]string{"service": "analytics", "code": "200", "role": "worker"})
	if len(metrics.counters) != 0 || len(metrics.observations) != 0 {
		t.Fatalf("неизвестные метки создали серии: counters=%d observations=%d", len(metrics.counters), len(metrics.observations))
	}
	metrics.Incr("http_requests_total", map[string]string{"service": "lead-management", "code": "200"})
	metrics.Observe("jobs_duration_seconds", 1, map[string]string{"service": "analytics"})
	if len(metrics.counters) != 1 || len(metrics.observations) != 1 {
		t.Fatalf("известные значения не учтены: counters=%d observations=%d", len(metrics.counters), len(metrics.observations))
	}
}

func TestMemoryMetricsRejectsInvalidTupleAndNonFiniteObservations(t *testing.T) {
	metrics := NewMetricsWithCatalog(metriccatalog.Default())
	valid := map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}
	invalidTuple := map[string]string{"port": "lead-search", "adapter": "smtp", "operation": "publish", "outcome": "success"}
	metrics.Incr("port_invocations_total", invalidTuple)
	if len(metrics.counters) != 0 {
		t.Fatalf("недопустимый кортеж создал серию: %+v", metrics.counters)
	}
	metrics.Observe("jobs_duration_seconds", 2, map[string]string{"service": "analytics"})
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		metrics.Observe("jobs_duration_seconds", value, map[string]string{"service": "analytics"})
		metrics.Observe("jobs_duration_seconds", value, map[string]string{"service": "lead-management"})
	}
	sum, count := metrics.Observation("jobs_duration_seconds", map[string]string{"service": "analytics"})
	if sum != 2 || count != 1 {
		t.Fatalf("non-finite загрязнили существующую серию: sum=%v count=%d", sum, count)
	}
	if _, count := metrics.Observation("jobs_duration_seconds", map[string]string{"service": "lead-management"}); count != 0 {
		t.Fatalf("non-finite создали новую серию: count=%d", count)
	}
	metrics.Incr("port_invocations_total", valid)
	if metrics.Counter("port_invocations_total", valid) != 1 {
		t.Fatal("допустимый tuple не принят")
	}
}
