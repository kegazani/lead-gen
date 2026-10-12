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

	httpapi "lead-gen/backend/contexts/audience/internal/adapters/http"
	"lead-gen/backend/platform/readiness"
)

func TestReadyzCachesBoundedProbeAndSanitizesFailure(t *testing.T) {
	var calls atomic.Int32
	evaluator := readiness.New(readiness.Config{Timeout: 20 * time.Millisecond, TTL: time.Second})
	server := httpapi.NewServer(httpapi.Dependencies{Readiness: func(ctx context.Context) httpapi.ReadinessReport {
		snapshot := evaluator.Evaluate(ctx, readiness.Profile{Capability: "lead-segmentation", Checks: []readiness.CheckSpec{
			{ID: "persistence", Key: "audience.test-db", Required: true,
				Probe: func(ctx context.Context) (readiness.ProbeResult, error) {
					calls.Add(1)
					<-ctx.Done()
					return readiness.ProbeResult{}, context.DeadlineExceeded
				}},
			{ID: "provider", Key: "audience.test-provider", Required: false,
				Probe: func(context.Context) (readiness.ProbeResult, error) {
					return readiness.ProbeResult{}, errors.New("https://user:secret@example.invalid/?token=credentials")
				}},
		}})
		profile := httpapi.ReadinessProfile{Capability: snapshot.Capability, Status: snapshot.Status, Ready: snapshot.Ready}
		for _, check := range snapshot.Checks {
			profile.Checks = append(profile.Checks, httpapi.ReadinessCheck{ID: check.ID, Status: check.Status, Reason: check.Reason, ObservedAt: check.ObservedAt, Required: check.Required})
		}
		return httpapi.ReadinessReport{Role: "api", Profiles: []httpapi.ReadinessProfile{profile}, Status: snapshot.Status, Ready: snapshot.Ready}
	}})
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"reason":"probe_timeout"`) || !strings.Contains(recorder.Body.String(), `"reason":"probe_failed"`) {
			t.Fatalf("/readyz response = %d %s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "credentials") {
			t.Fatalf("ошибка провайдера раскрыта в ответе: %s", recorder.Body.String())
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("TTL cache вызвал probe %d раз, ожидался 1", got)
	}
}
