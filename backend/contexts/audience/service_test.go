package audience_test

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/audience"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// newService собирает сервис с наблюдаемостью и без издателя.
func newService(t *testing.T) *audience.Service {
	t.Helper()
	service, err := audience.New(audience.Config{
		APIAddr:  "127.0.0.1:0",
		Interval: 10 * time.Millisecond,
		Logger:   observ.NewLogger(),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer(audience.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	return service
}

// TestServiceID — идентификатор сервиса совпадает с идентификатором контекста.
func TestServiceID(t *testing.T) {
	if got := newService(t).ID(); got != "audience" {
		t.Fatalf("ID = %q, ожидался audience", got)
	}
}

// TestRoleRunnersDeclared — все объявленные каталогом роли получают раннер, а
// прочие роли контексту не принадлежат.
func TestRoleRunnersDeclared(t *testing.T) {
	service := newService(t)
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleWorker, composition.RoleSchedule} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %q не объявлена сервисом", role)
		}
	}
	if _, ok := service.RoleRunner(composition.RoleChains); ok {
		t.Fatal("роль chains не должна принадлежать контексту audience")
	}
}

// TestScheduleRunnerStopsOnCancel — раннер schedule честно пересчитывает и
// завершается по отмене контекста.
func TestScheduleRunnerStopsOnCancel(t *testing.T) {
	service := newService(t)
	runner, ok := service.RoleRunner(composition.RoleSchedule)
	if !ok {
		t.Fatal("роль schedule не объявлена")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("работа schedule: %v", err)
	}
}

// TestWorkerRunnerHonestWithoutPublisher — без издателя worker не притворяется
// публикующим: роль держится до отмены и завершается штатно.
func TestWorkerRunnerHonestWithoutPublisher(t *testing.T) {
	service := newService(t)
	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("роль worker не объявлена")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("работа worker: %v", err)
	}
}
