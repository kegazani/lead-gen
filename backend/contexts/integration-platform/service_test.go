package integrationplatform

import (
	"context"
	"io"
	"testing"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/provider/fake"
	"lead-gen/backend/contexts/integration-platform/internal/app"
	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

var testTime = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

func newTestService(t *testing.T, provider ports.ProviderPort) *Service {
	t.Helper()
	service, err := New(Config{
		Provider: provider,
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	return service
}

// Сервис собирается, сообщает свой идентификатор и умеет роли из каталога.
func TestService_Roles(t *testing.T) {
	service := newTestService(t, nil)
	if service.ID() != ServiceID {
		t.Fatalf("идентификатор сервиса: %q", service.ID())
	}
	for _, role := range []composition.Role{
		composition.RoleAPI,
		composition.RoleIngest,
		composition.RoleWorker,
		composition.RoleChains,
		composition.RoleSchedule,
	} {
		runner, ok := service.RoleRunner(role)
		if !ok {
			t.Fatalf("роль %q не заявлена", role)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := runner.Run(ctx); err != nil {
			t.Fatalf("роль %q: %v", role, err)
		}
	}
	if _, ok := service.RoleRunner(composition.RoleSender); ok {
		t.Fatal("роль sender в срезе не реализована и не должна заявляться")
	}
}

// Сквозной путь среза: регистрация адаптера → вызов через порт → запись вызова.
func TestService_EndToEnd(t *testing.T) {
	provider := fake.New(fake.Config{
		Scenario: fake.Scenario{Kind: fake.OutcomeSuccess, ExternalID: "ext-1"},
	})
	service := newTestService(t, provider)
	ctx := context.Background()

	if _, err := service.slice.RegisterAdapter.Handle(ctx, app.RegisterAdapterCommand{
		AdapterID:       "adapter-1",
		AdapterName:     "firecrawl",
		ManifestVersion: "1.0.0",
		Capabilities:    []domain.CapabilityName{domain.CapabilitySearch},
		RegisteredAt:    testTime,
	}); err != nil {
		t.Fatalf("регистрация адаптера: %v", err)
	}

	result, err := service.slice.InvokeIntegration.Handle(ctx, app.InvokeIntegrationCommand{
		InvocationID:    "inv-1",
		EventID:         "evt-1",
		OccurredAt:      testTime,
		TenantID:        "tenant-1",
		CorrelationRef:  "corr-1",
		IntegrationType: "web-research",
		Operation:       "search",
		CommandKey:      "cmd-1",
	})
	if err != nil {
		t.Fatalf("вызов интеграции: %v", err)
	}
	if result.State != domain.InvocationSucceeded {
		t.Fatalf("состояние: %q", result.State)
	}
	if result.ExternalRef == nil || result.ExternalRef.ExternalID != "ext-1" {
		t.Fatalf("привязка внешнего id: %+v", result.ExternalRef)
	}
}
