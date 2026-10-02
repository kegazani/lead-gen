package datagovernance_test

import (
	"context"
	"io"
	"testing"
	"time"

	datagovernance "lead-gen/backend/contexts/data-governance"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// newService собирает сервис на in-memory адаптерах с тихим логгером.
func newService(t *testing.T) *datagovernance.Service {
	t.Helper()
	service, err := datagovernance.New(datagovernance.Config{
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(datagovernance.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	return service
}

// TestServiceID проверяет соответствие идентификатора сервиса контексту.
func TestServiceID(t *testing.T) {
	t.Parallel()

	if got := newService(t).ID(); got != "data-governance" {
		t.Fatalf("ID = %q", got)
	}
}

// TestRoleRunner проверяет, что каждая объявленная каталогом роль получает
// раннер, а необъявленная — нет.
func TestRoleRunner(t *testing.T) {
	t.Parallel()

	service := newService(t)
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleWorker, composition.RoleChains} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %q не получила раннер", role)
		}
	}
	if _, ok := service.RoleRunner(composition.RoleSender); ok {
		t.Fatal("необъявленная роль получила раннер")
	}
}

// TestWorkerRunnerRuns проверяет, что раннер роли worker запускается и штатно
// завершается по отмене контекста.
func TestWorkerRunnerRuns(t *testing.T) {
	t.Parallel()

	service := newService(t)
	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("роль worker не получила раннер")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("раннер worker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("раннер worker не завершился по отмене контекста")
	}
}
