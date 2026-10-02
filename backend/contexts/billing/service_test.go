package billing_test

import (
	"context"
	"io"
	"testing"
	"time"

	billing "lead-gen/backend/contexts/billing"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// TestServiceRequiresObservability проверяет, что сборка сервиса требует логгер,
// метрики и трейсер.
func TestServiceRequiresObservability(t *testing.T) {
	if _, err := billing.New(billing.Config{}); err == nil {
		t.Fatal("сборка без наблюдаемости должна отвергаться")
	}
	if _, err := billing.New(billing.Config{
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(),
	}); err == nil {
		t.Fatal("сборка без трейсера должна отвергаться")
	}
}

// TestServiceRoleRunners проверяет, что сервис публикует роли api и worker и
// отклоняет чужие.
func TestServiceRoleRunners(t *testing.T) {
	service := newTestService(t)
	if service.ID() != billing.ServiceID {
		t.Fatalf("идентификатор сервиса = %q", service.ID())
	}
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleWorker} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %s должна быть реализована", role)
		}
	}
	if _, ok := service.RoleRunner(composition.RoleSchedule); ok {
		t.Fatal("роль schedule не объявлена контекстом")
	}
}

// TestWorkerStopsOnCancel проверяет, что раннер роли worker завершается по отмене
// контекста.
func TestWorkerStopsOnCancel(t *testing.T) {
	service := newTestService(t)
	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("роль worker должна быть реализована")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("раннер worker вернул ошибку: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("раннер worker не завершился по отмене контекста")
	}
}

// newTestService собирает сервис с детерминированными зависимостями и без
// платёжной системы.
func newTestService(t *testing.T) *billing.Service {
	t.Helper()
	service, err := billing.New(billing.Config{
		Interval: 10 * time.Millisecond,
		Clock:    &fixedClock{now: testNow},
		IDs:      &seqIDs{},
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer(billing.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	return service
}
