package outreachorchestration_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"lead-gen/backend/platform/observability/observ"
)

// TestRequestObservability проверяет, что входящий запрос среза через
// observ.Middleware порождает лог с correlation_id и инкрементирует метрики
// запроса с меткой сервиса.
func TestRequestObservability(t *testing.T) {
	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	tracer := observ.NewTracer("outreach-orchestration")

	fx := newE2EFixture(t)
	handler := observ.Middleware(logger, metrics, tracer, "outreach-orchestration")(fx.slice.Server)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	code := strconv.Itoa(rec.Code)
	if got := metrics.Counter("http_requests_total", map[string]string{"service": "outreach-orchestration", "code": code}); got != 1 {
		t.Fatalf("http_requests_total{code=%s} = %d, ожидался 1", code, got)
	}
	if !strings.Contains(buf.String(), `"correlation_id":"`) {
		t.Fatalf("лог запроса не содержит correlation_id: %s", buf.String())
	}
}
