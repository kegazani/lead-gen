package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/platform/readiness"
)

func TestReadyzCacheTimeoutAndSafeResponse(t *testing.T) {
	var calls atomic.Int32
	evaluator := readiness.New(readiness.Config{Timeout: 20 * time.Millisecond, TTL: time.Second})
	server := httpapi.NewServer(httpapi.Dependencies{Readiness: func(ctx context.Context) (int, any) {
		snapshot := evaluator.Evaluate(ctx, readiness.Profile{Capability: "lead-management-core", Checks: []readiness.CheckSpec{
			{ID: "database", Key: "lead-management.test-db", Required: true,
				Probe: func(ctx context.Context) (readiness.ProbeResult, error) {
					calls.Add(1)
					<-ctx.Done()
					return readiness.ProbeResult{}, context.DeadlineExceeded
				}},
			{ID: "provider", Key: "lead-management.test-provider", Required: false,
				Probe: func(context.Context) (readiness.ProbeResult, error) {
					return readiness.ProbeResult{}, errors.New("https://user:secret@example.invalid/?token=credentials")
				}},
		}})
		status := http.StatusOK
		if !snapshot.Ready {
			status = http.StatusServiceUnavailable
		}
		return status, snapshot
	}})
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"Reason":"probe_timeout"`) || !strings.Contains(recorder.Body.String(), `"Reason":"probe_failed"`) {
			t.Fatalf("/readyz response = %d %s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "credentials") {
			t.Fatalf("сырой текст probe попал в ответ: %s", recorder.Body.String())
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe вызван %d раз при TTL cache", got)
	}
	for _, path := range []string{"/healthz"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("сохранённый liveness маршрут %s: HTTP %d", path, recorder.Code)
		}
	}
}
