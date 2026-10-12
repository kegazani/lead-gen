package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/observability/prommetrics"
)

// TestMetricsListenerServesPrometheusFormat проверяет сценарий спеки «скрейп
// /metrics возвращает метрики» на процесс-уровневой точке сборки: реальный
// слушатель startMetricsListener отдаёт GET /metrics в формате Prometheus
// exposition с Content-Type text/plain; version=0.0.4, а учтённые через порт
// observ.Metrics образцы http_requests_total и jobs_total несут метку service.
func TestMetricsListenerServesPrometheusFormat(t *testing.T) {
	adapter := prommetrics.New()
	adapter.Incr("http_requests_total", map[string]string{"service": "outreach-orchestration", "code": "200"})
	adapter.Incr("jobs_total", map[string]string{"service": "outreach-orchestration", "outcome": "done"})

	addr := freeListenAddr(t)
	srv, err := startMetricsListener(addr, adapter.Handler(), observ.NewLoggerWithWriter(io.Discard))
	if err != nil {
		t.Fatalf("слушатель метрик не поднят: %v", err)
	}
	if srv == nil {
		t.Fatal("слушатель метрик не поднят (nil)")
	}
	defer func() { _ = srv.Close() }()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d, ожидался 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4") {
		t.Fatalf("Content-Type = %q, ожидался text/plain; version=0.0.4", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела /metrics: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"http_requests_total",
		"jobs_total",
		`service="outreach-orchestration"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("тело /metrics не содержит %q:\n%s", want, text)
		}
	}
}

// TestMetricsListenerEmptyAddrDisabled проверяет, что пустой адрес отключает
// экспорт метрик: слушатель не поднимается и ошибки нет (конфигурация, а не сбой).
func TestMetricsListenerEmptyAddrDisabled(t *testing.T) {
	srv, err := startMetricsListener("", http.NotFoundHandler(), observ.NewLoggerWithWriter(io.Discard))
	if err != nil {
		t.Fatalf("пустой адрес не должен возвращать ошибку: %v", err)
	}
	if srv != nil {
		_ = srv.Close()
		t.Fatal("при пустом адресе слушатель не должен подниматься")
	}
}

// TestMetricsListenerOnOccupiedAddr проверяет, что занятый адрес метрик
// возвращается вызывающему как ошибка (в main — фатальный выход): адрес экспорта
// — часть контракта запуска, тихо жить без метрик нельзя.
func TestMetricsListenerOnOccupiedAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("не удалось занять адрес: %v", err)
	}
	defer func() { _ = ln.Close() }()

	srv, err := startMetricsListener(ln.Addr().String(), http.NotFoundHandler(), observ.NewLoggerWithWriter(io.Discard))
	if srv != nil {
		_ = srv.Close()
		t.Fatal("при занятом адресе слушатель не должен возвращаться")
	}
	if err == nil {
		t.Fatal("занятый адрес не вернул ошибку привязки")
	}
}
