package publishing_test

import (
	"context"
	"io"
	"testing"

	publishing "lead-gen/backend/contexts/publishing"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

func TestServiceRequiresObservability(t *testing.T) {
	if _, err := publishing.New(publishing.Config{}); err == nil {
		t.Fatal("ожидалась ошибка при отсутствии Logger")
	}
}

func TestServiceRoles(t *testing.T) {
	service, err := publishing.New(publishing.Config{
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer("publishing"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if service.ID() != "publishing" {
		t.Fatalf("ID = %q, ожидался publishing", service.ID())
	}
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleWorker, composition.RoleSchedule} {
		runner, ok := service.RoleRunner(role)
		if !ok || runner == nil {
			t.Fatalf("роль %q не реализована", role)
		}
	}
	if _, ok := service.RoleRunner(composition.RoleIngest); ok {
		t.Fatal("роль ingest не должна поддерживаться")
	}
}

func TestWorkerAndScheduleStopOnCancel(t *testing.T) {
	service, err := publishing.New(publishing.Config{
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer("publishing"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, role := range []composition.Role{composition.RoleWorker, composition.RoleSchedule} {
		runner, _ := service.RoleRunner(role)
		if err := runner.Run(ctx); err != nil {
			t.Fatalf("роль %q: %v", role, err)
		}
	}
}
