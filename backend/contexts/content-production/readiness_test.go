package contentproduction

import (
	"context"
	"testing"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

type rendererReadinessFake struct{ calls int }

func (r *rendererReadinessFake) ReadinessCheck(context.Context) error { r.calls++; return nil }

func TestRendererReadinessUsesBoundedHealthContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	renderer := &rendererReadinessFake{}
	report := newReadinessReporter(Persistence{Ping: func(context.Context) error { return nil }}, renderer, true)
	worker := report.RoleReadiness(ctx, composition.RoleWorker)
	if worker.Ready || worker.Status != readiness.StatusUnknown {
		t.Fatalf("отменённая проверка не готова: %+v", worker)
	}
	if renderer.calls != 0 {
		t.Fatalf("отменённый probe вызвал renderer %d раз", renderer.calls)
	}

	worker = report.RoleReadiness(context.Background(), composition.RoleWorker)
	if !worker.Ready || renderer.calls != 1 {
		t.Fatalf("пассивный health-check renderer не учтён: report=%+v calls=%d", worker, renderer.calls)
	}
}

func TestInMemoryPersistenceIsNotReady(t *testing.T) {
	report := newReadinessReporter(Persistence{}, nil, false)
	api := report.RoleReadiness(context.Background(), composition.RoleAPI)
	if api.Ready || api.Status != readiness.StatusUnknown {
		t.Fatalf("in-memory режим не должен подтверждать production readiness: %+v", api)
	}
}
