package audience

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/platform/readiness"
)

func TestReadinessProfilesDistinguishAPIAndWorker(t *testing.T) {
	service := &Service{
		persistence: Persistence{DatabaseProbe: func(context.Context) (readiness.ProbeResult, error) {
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}},
		readinessEvaluator: readiness.New(readiness.Config{Timeout: 100 * time.Millisecond, TTL: time.Second}),
	}
	api := service.Readiness(context.Background(), "api")
	if !api.Ready || api.Status != readiness.StatusUp || len(api.Profiles) != 1 || api.Profiles[0].Capability != "lead-segmentation" {
		t.Fatalf("API должен проверять только обязательную persistence: %+v", api)
	}
	worker := service.Readiness(context.Background(), "worker")
	if !worker.Ready || worker.Status != readiness.StatusDegraded || len(worker.Profiles) != 2 || worker.Profiles[1].Status != readiness.StatusDegraded {
		t.Fatalf("непроверенный publisher должен быть видимым degraded, не healthy: %+v", worker)
	}
	down := &Service{
		persistence: Persistence{DatabaseProbe: func(context.Context) (readiness.ProbeResult, error) {
			return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
		}},
		readinessEvaluator: readiness.New(readiness.Config{Timeout: 100 * time.Millisecond, TTL: time.Second}),
	}
	if report := down.Readiness(context.Background(), "api"); report.Ready || report.Status != readiness.StatusDown {
		t.Fatalf("DB down должен блокировать API: %+v", report)
	}
}
