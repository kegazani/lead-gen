package analytics_test

import (
	"io"
	"reflect"
	"strings"
	"testing"

	analytics "lead-gen/backend/contexts/analytics"
	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// TestServiceRegistersRoles проверяет идентификатор сервиса и раннеры ролей:
// api и worker зарегистрированы, schedule — нет.
func TestServiceRegistersRoles(t *testing.T) {
	service, err := analytics.New(analytics.Config{
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(analytics.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	if service.ID() != "analytics" {
		t.Fatalf("ID = %q, ожидался analytics", service.ID())
	}
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleWorker} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %q не зарегистрирована", role)
		}
	}
	if _, ok := service.RoleRunner(composition.RoleSchedule); ok {
		t.Fatalf("роль schedule не должна быть зарегистрирована")
	}
}

// TestNewRequiresObservability проверяет, что сборка без наблюдаемости падает.
func TestNewRequiresObservability(t *testing.T) {
	if _, err := analytics.New(analytics.Config{}); err == nil {
		t.Fatalf("сборка без наблюдаемости должна возвращать ошибку")
	}
}

// TestNoOpenMetrics проверяет, что моделей открытий нет вообще: ни одного поля,
// похожего на метрику открытий, в read-модели и представлениях дашборда.
func TestNoOpenMetrics(t *testing.T) {
	assertNoFieldLike(t, readmodel.Snapshot{}, "open")
	assertNoFieldLike(t, readmodel.GrowthPoint{}, "open")
	assertNoFieldLike(t, query.DashboardView{}, "open")
	assertNoFieldLike(t, query.ProgressView{}, "open")
	assertNoFieldLike(t, query.OperationsView{}, "open")
}

// assertNoFieldLike проверяет, что у типа нет поля, в имени которого встречается
// подстрока (регистр не важен).
func assertNoFieldLike(t *testing.T, value any, substr string) {
	t.Helper()
	typ := reflect.TypeOf(value)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if strings.Contains(strings.ToLower(field.Name), substr) {
			t.Fatalf("тип %s содержит поле %q, похожее на метрику открытий", typ.Name(), field.Name)
		}
	}
}
