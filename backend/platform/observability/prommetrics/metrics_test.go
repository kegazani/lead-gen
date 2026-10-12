package prommetrics

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/platform/observability/metriccatalog"
	"lead-gen/backend/platform/observability/observ"
)

// TestMetricsHandlerExposesHTTPRequests проверяет, что образцы, учтённые через
// порт observ.Metrics, доступны на /metrics в формате Prometheus exposition.
func TestMetricsHandlerExposesHTTPRequests(t *testing.T) {
	a := New()

	a.Incr("http_requests_total", map[string]string{"service": "outreach-orchestration", "code": "200"})
	a.Incr("http_requests_total", map[string]string{"service": "analytics", "code": "500"})
	a.Incr("jobs_total", map[string]string{"service": "outreach-orchestration", "outcome": "done"})
	a.Observe("http_duration_seconds", 0.05, map[string]string{"service": "outreach-orchestration"})

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4") {
		t.Fatalf("Content-Type = %q, ожидался text/plain; version=0.0.4", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "http_requests_total") {
		t.Fatalf("тело /metrics не содержит http_requests_total:\n%s", body)
	}
	// Сценарий спеки требует в теле /metrics и http_requests_total, и jobs_total.
	if !strings.Contains(body, "jobs_total") {
		t.Fatalf("тело /metrics не содержит jobs_total:\n%s", body)
	}
	if !strings.Contains(body, "http_duration_seconds") {
		t.Fatalf("тело /metrics не содержит http_duration_seconds:\n%s", body)
	}
	// Метка service различает сервисы одной группы.
	if !strings.Contains(body, `service="outreach-orchestration"`) || !strings.Contains(body, `service="analytics"`) {
		t.Fatalf("метка service не различает сервисы:\n%s", body)
	}
}

// TestAdapterRejectsInvalidLabels проверяет, что недопустимые метки и неизвестные
// имена метрик не создают временной серии в экспозиции.
func TestAdapterRejectsInvalidLabels(t *testing.T) {
	a := New()

	// Идентификатор сущности в метках не допускается и не появляется в /metrics.
	a.Incr("http_requests_total", map[string]string{"service": "analytics", "workflow_id": "wf-1"})
	// Произвольный идентификатор пользователя (high-cardinality) отбрасывается.
	a.Incr("http_requests_total", map[string]string{"service": "analytics", "user_id": "u-42"})
	// Неизвестное имя метрики отбрасывается.
	a.Incr("unknown_metric_total", map[string]string{"service": "analytics"})
	// Значение-URL не допускается.
	a.Incr("downstream_errors_total", map[string]string{"service": "https://example.com", "engine": "llm"})

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()

	if strings.Contains(body, "wf-1") {
		t.Fatalf("идентификатор попал в метрики:\n%s", body)
	}
	if strings.Contains(body, "u-42") {
		t.Fatalf("идентификатор пользователя попал в метрики:\n%s", body)
	}
	if strings.Contains(body, "unknown_metric_total") {
		t.Fatalf("неизвестная метрика появилась в экспозиции:\n%s", body)
	}
	if strings.Contains(body, "https://example.com") {
		t.Fatalf("URL попал в метрики:\n%s", body)
	}
}

// TestAdapterRecordsHistogram проверяет, что наблюдения через адаптер фиксируются
// реальной гистограммой (не заглушкой): наблюдение меняет счётчики бакетов.
func TestAdapterRecordsHistogram(t *testing.T) {
	a := New()

	a.Observe("jobs_duration_seconds", 1.5, map[string]string{"service": "analytics"})

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "jobs_duration_seconds_bucket") {
		t.Fatalf("гистограмма не отдала бакеты:\n%s", body)
	}
}

func TestAdapterClosedCatalogAndMemoryParity(t *testing.T) {
	catalog := metriccatalog.Default()
	a := NewWithCatalog(catalog)
	type testSample struct {
		metric string
		kind   metriccatalog.Kind
		labels map[string]string
	}
	fields := map[string]testSample{
		"service":   {metric: "http_duration_seconds", kind: metriccatalog.Histogram, labels: map[string]string{"service": "lead-management"}},
		"role":      {metric: "role_exit_live_total", kind: metriccatalog.Counter, labels: map[string]string{"service": "analytics", "role": "worker"}},
		"port":      {metric: "port_invocations_total", kind: metriccatalog.Counter, labels: map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}},
		"adapter":   {metric: "port_invocations_total", kind: metriccatalog.Counter, labels: map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}},
		"operation": {metric: "port_invocations_total", kind: metriccatalog.Counter, labels: map[string]string{"port": "lead-search", "adapter": "http", "operation": "search", "outcome": "success"}},
		"engine":    {metric: "downstream_errors_total", kind: metriccatalog.Counter, labels: map[string]string{"service": "lead-management", "engine": "search"}},
	}
	for label, valid := range fields {
		for _, bad := range []string{"a@b.co", "550e8400-e29b-41d4-a716-446655440000", "tok_abc123", "unregistered"} {
			labels := cloneLabels(valid.labels)
			labels[label] = bad
			if valid.kind == metriccatalog.Histogram {
				a.Observe(valid.metric, 1, labels)
			} else {
				a.Incr(valid.metric, labels)
			}
		}
	}
	// Для каждого schema неизвестное имя, отсутствующие и лишние labels отвергаются.
	a.Incr("dynamic_metric_total", map[string]string{"service": "analytics"})
	a.Incr("http_requests_total", map[string]string{"service": "analytics"})
	a.Incr("http_requests_total", map[string]string{"service": "analytics", "code": "200", "role": "worker"})
	a.Incr("port_invocations_total", map[string]string{"port": "lead-search", "adapter": "smtp", "operation": "publish", "outcome": "success"})
	a.Observe("jobs_duration_seconds", 1, map[string]string{"service": "analytics", "extra": "x"})

	a.Observe("http_duration_seconds", 1, fields["service"].labels)
	a.Incr("port_invocations_total", fields["port"].labels)
	a.Incr("role_exit_live_total", fields["role"].labels)
	a.Incr("downstream_errors_total", fields["engine"].labels)

	families, err := a.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, family := range families {
		counts[family.GetName()] = len(family.GetMetric())
	}
	if counts["port_invocations_total"] != 1 || counts["role_exit_live_total"] != 1 || counts["downstream_errors_total"] != 1 || counts["http_duration_seconds"] != 1 || counts["http_requests_total"] != 0 || counts["jobs_duration_seconds"] != 0 {
		t.Fatalf("отказанные значения увеличили серии или известные не приняты: %+v", counts)
	}

	memory := observ.NewMetricsWithCatalog(catalog)
	for label, valid := range fields {
		for _, bad := range []string{"a@b.co", "550e8400-e29b-41d4-a716-446655440000", "tok_abc123", "unregistered"} {
			labels := cloneLabels(valid.labels)
			labels[label] = bad
			if valid.kind == metriccatalog.Histogram {
				memory.Observe(valid.metric, 1, labels)
				if _, count := memory.Observation(valid.metric, labels); count != 0 {
					t.Errorf("MemoryMetrics учёл %s=%q", label, bad)
				}
			} else {
				memory.Incr(valid.metric, labels)
				if memory.Counter(valid.metric, labels) != 0 {
					t.Errorf("MemoryMetrics учёл %s=%q", label, bad)
				}
			}
		}
	}
	memory.Observe("http_duration_seconds", 1, fields["service"].labels)
	memory.Incr("port_invocations_total", fields["port"].labels)
	memory.Incr("role_exit_live_total", fields["role"].labels)
	memory.Incr("downstream_errors_total", fields["engine"].labels)
	if memory.Counter("port_invocations_total", fields["port"].labels) != 1 || memory.Counter("role_exit_live_total", fields["role"].labels) != 1 || memory.Counter("downstream_errors_total", fields["engine"].labels) != 1 {
		t.Fatal("MemoryMetrics отвергла допустимые port/role/downstream метки")
	}
	if _, count := memory.Observation("http_duration_seconds", fields["service"].labels); count != 1 {
		t.Fatal("MemoryMetrics отвергла допустимую service-метку")
	}
}

func TestAdapterRejectsNonFiniteObservations(t *testing.T) {
	catalog := metriccatalog.Default()
	a := NewWithCatalog(catalog)
	labels := map[string]string{"service": "analytics"}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		a.Observe("jobs_duration_seconds", value, labels)
		a.Observe("jobs_duration_seconds", value, map[string]string{"service": "lead-management"})
	}
	a.Observe("jobs_duration_seconds", 2, labels)

	families, err := a.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "jobs_duration_seconds" {
			continue
		}
		if len(family.GetMetric()) != 1 {
			t.Fatalf("non-finite создали лишнюю серию: %d", len(family.GetMetric()))
		}
		metric := family.GetMetric()[0]
		if metric.GetHistogram().GetSampleCount() != 1 || metric.GetHistogram().GetSampleSum() != 2 {
			t.Fatalf("non-finite изменили существующее наблюдение: count=%d sum=%v", metric.GetHistogram().GetSampleCount(), metric.GetHistogram().GetSampleSum())
		}
		return
	}
	t.Fatal("серия конечного наблюдения не зарегистрирована")
}

func cloneLabels(labels map[string]string) map[string]string {
	copy := make(map[string]string, len(labels))
	for key, value := range labels {
		copy[key] = value
	}
	return copy
}
