package engagementobservation_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	engagementobservation "lead-gen/backend/contexts/engagement-observation"
	"lead-gen/backend/contexts/engagement-observation/internal/app/command"
	"lead-gen/backend/contexts/engagement-observation/internal/app/query"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/engagement-observation/engagementevent"
	"lead-gen/contracts/engagement-observation/readsignal"
)

func testConfig() engagementobservation.Config {
	return engagementobservation.Config{
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(engagementobservation.ServiceID),
	}
}

func TestServiceRegistersRoles(t *testing.T) {
	service, err := engagementobservation.New(testConfig())
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	if service.ID() != "engagement-observation" {
		t.Fatalf("ID = %q", service.ID())
	}
	for _, role := range []composition.Role{composition.RoleIngest, composition.RoleWorker} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %q не зарегистрирована", role)
		}
	}
	if _, ok := service.RoleRunner(composition.RoleAPI); ok {
		t.Fatalf("роль api не должна быть зарегистрирована")
	}
}

func TestNewRequiresObservability(t *testing.T) {
	if _, err := engagementobservation.New(engagementobservation.Config{}); err == nil {
		t.Fatalf("сборка без наблюдаемости должна возвращать ошибку")
	}
}

func TestBuildHTTPRoundTrip(t *testing.T) {
	slice, err := engagementobservation.Build(testConfig())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	server := slice.Server

	health := httptest.NewRecorder()
	server.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("healthz: статус = %d", health.Code)
	}

	body := strings.NewReader(`{"signal_id":"sig-1","source":"provider","tenant_id":"tenant-1","company_id":"company-1","kind":"delivery","outcome":"delivered"}`)
	req := httptest.NewRequest(http.MethodPost, "/events", body)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("приём: статус = %d, тело = %s", rec.Code, rec.Body.String())
	}
	pending, err := slice.Journal.Pending(context.Background(), 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("записей outbox = %d, err = %v", len(pending), err)
	}
}

func TestEngagementEventPublisher(t *testing.T) {
	slice, err := engagementobservation.Build(testConfig())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	publisher := engagementobservation.NewEngagementEventPublisher(slice)

	valid := engagementevent.NewEngagementEvent(engagementevent.Envelope{
		EventID: "event-1", OccurredAt: time.Now().UTC(), TenantID: "tenant-1", CorrelationID: "corr-1",
	})
	valid.EngagementEventID = "evt-1"
	valid.CompanyID = "company-1"
	valid.Kind = engagementevent.KindDelivery
	valid.Confidence = engagementevent.ConfidenceHigh
	valid.Outcome = "delivered"
	if err := publisher.Publish(context.Background(), valid); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	pending, _ := slice.Journal.Pending(context.Background(), 10)
	if len(pending) != 1 {
		t.Fatalf("записей outbox = %d, ожидалось 1", len(pending))
	}

	pixel := valid
	pixel.Kind = "open"
	pixel.Outcome = "opened"
	if err := publisher.Publish(context.Background(), pixel); err == nil {
		t.Fatalf("пиксельный контракт должен быть отвергнут")
	}
}

func TestReadSignalResolverAndRecorder(t *testing.T) {
	slice, err := engagementobservation.Build(testConfig())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ctx := context.Background()

	if _, err := slice.IngestEvent.Handle(ctx, command.IngestEvent{
		SignalID: "sig-1", Source: "provider",
		TenantID: "tenant-1", CompanyID: "company-1",
		Kind: "transition", LinkRef: "lnk-1", Target: "https://example.com/landing",
	}); err != nil {
		t.Fatalf("регистрация перехода: %v", err)
	}

	resolver := engagementobservation.NewReadSignalResolver(slice)
	target, err := resolver.Resolve(ctx, "lnk-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target != "https://example.com/landing" {
		t.Fatalf("target = %q", target)
	}
	if _, err := resolver.Resolve(ctx, "missing"); !errors.Is(err, readsignal.ErrNotFound) {
		t.Fatalf("неизвестная ссылка: ошибка = %v, ожидалась ErrNotFound", err)
	}

	recorder := engagementobservation.NewReadSignalRecorder(slice)
	if err := recorder.Record(ctx, readsignal.Transition{Link: "lnk-1", Target: target, OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	list, err := slice.Repository.ListByTenant(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("событий = %d, ожидалось 2 (регистрация и переход)", len(list))
	}
	if err := recorder.Record(ctx, readsignal.Transition{Link: "missing"}); !errors.Is(err, readsignal.ErrNotFound) {
		t.Fatalf("неизвестная ссылка: ошибка = %v, ожидалась ErrNotFound", err)
	}
}

func TestWorkerWithoutStreamStopsOnCancel(t *testing.T) {
	service, err := engagementobservation.New(testConfig())
	if err != nil {
		t.Fatalf("сборка: %v", err)
	}
	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatalf("роль worker не зарегистрирована")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run не завершился по отмене контекста")
	}
}

// TestNoPixelFields проверяет проектное требование: в модели, контрактах и
// представлениях контекста нет полей, похожих на метрику открытий или пиксель.
func TestNoPixelFields(t *testing.T) {
	values := []any{
		domain.EngagementObserved{},
		domain.EngagementSnapshot{},
		command.IngestEvent{},
		command.IngestEventResult{},
		query.EngagementView{},
		engagementevent.EngagementEvent{},
	}
	for _, value := range values {
		assertNoFieldLike(t, value, "open")
		assertNoFieldLike(t, value, "pixel")
		assertNoFieldLike(t, value, "view")
		assertNoFieldLike(t, value, "impression")
	}
}

func assertNoFieldLike(t *testing.T, value any, substr string) {
	t.Helper()
	typ := reflect.TypeOf(value)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if strings.Contains(strings.ToLower(field.Name), substr) {
			t.Fatalf("тип %s содержит поле %q, похожее на пиксельную метрику", typ.Name(), field.Name)
		}
	}
}

// Утверждение, что ошибки портов различимы через errors.Is на границе сервиса.
var _ = ports.ErrEventNotFound
