package observ

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// flushRecorder считает вызовы Flush, прошедшие через обёртку middleware.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

// TestMiddlewareForwardsFlush проверяет, что потоковый ответ может сбрасывать
// буфер сквозь обёртку наблюдения: иначе text/event-stream рвётся после первого
// кадра.
func TestMiddlewareForwardsFlush(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)
	metrics := NewMetrics().(*MemoryMetrics)
	tracer := NewTracer("analytics")

	h := Middleware(logger, metrics, tracer, "analytics")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)
			for i := 0; i < 2; i++ {
				fmt.Fprint(w, "x")
				if err := rc.Flush(); err != nil {
					t.Fatalf("Flush через обёртку: %v", err)
				}
			}
		}),
	)

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistant/stream", nil))

	if rec.flushes < 2 {
		t.Fatalf("вызовов Flush = %d, ожидалось не меньше 2", rec.flushes)
	}
}

func TestMiddlewareRequestWithoutHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)
	metrics := NewMetrics().(*MemoryMetrics)
	tracer := NewTracer("analytics")

	var gotCorrelation string
	var gotSpan Span
	var gotSpanOK bool

	h := Middleware(logger, metrics, tracer, "analytics")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, _ := FieldsFromContext(r.Context())
			gotCorrelation = f.CorrelationID
			gotSpan, gotSpanOK = SpanFromContext(r.Context())
			w.WriteHeader(http.StatusCreated)
		}),
	)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/cards", nil))

	if gotCorrelation == "" {
		t.Fatal("correlation_id не передан в контекст")
	}
	if !gotSpanOK || gotSpan.Name != "http.request" || gotSpan.ServiceName != "analytics" {
		t.Fatalf("спан = %+v ok=%v", gotSpan, gotSpanOK)
	}
	if got := metrics.Counter("http_requests_total", map[string]string{"service": "analytics", "code": "201"}); got != 1 {
		t.Fatalf("счётчик запросов = %d, ожидался 1", got)
	}
	if _, count := metrics.Observation("http_duration_seconds", map[string]string{"service": "analytics"}); count != 1 {
		t.Fatalf("наблюдений длительности = %d, ожидалось 1", count)
	}
	if !strings.Contains(buf.String(), gotCorrelation) {
		t.Fatalf("лог не несёт correlation_id: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"status":201`) {
		t.Fatalf("лог не несёт статус 201: %s", buf.String())
	}
}

func TestMiddlewareRequestWithHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)
	metrics := NewMetrics().(*MemoryMetrics)
	tracer := NewTracer("analytics")

	var gotCorrelation string

	h := Middleware(logger, metrics, tracer, "analytics")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, _ := FieldsFromContext(r.Context())
			gotCorrelation = f.CorrelationID
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(correlationIDHeader, "corr-123")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotCorrelation != "corr-123" {
		t.Fatalf("correlation_id = %q, ожидался переданный corr-123", gotCorrelation)
	}
	if !strings.Contains(buf.String(), `"correlation_id":"corr-123"`) {
		t.Fatalf("лог не несёт переданный correlation_id: %s", buf.String())
	}
}
