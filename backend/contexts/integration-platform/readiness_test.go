package integrationplatform

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/readiness"
)

type readinessRegistrationRepository struct{ err error }

func (r readinessRegistrationRepository) Save(context.Context, *domain.AdapterRegistration) error {
	return nil
}

func (r readinessRegistrationRepository) FindByID(context.Context, domain.AdapterID) (*domain.AdapterRegistration, error) {
	return nil, r.err
}

func (r readinessRegistrationRepository) FindByName(context.Context, domain.AdapterName) (*domain.AdapterRegistration, error) {
	return nil, r.err
}

func (r readinessRegistrationRepository) List(context.Context) ([]*domain.AdapterRegistration, error) {
	return nil, r.err
}

var _ ports.AdapterRegistrationRepository = readinessRegistrationRepository{}

func newReadinessTestService(t *testing.T, registrations ports.AdapterRegistrationRepository, adapters ports.AdapterStatusReader) *Service {
	t.Helper()
	service, err := New(Config{
		Registrations: registrations,
		Adapters:      adapters,
		Logger:        observ.NewLoggerWithWriter(io.Discard),
		Metrics:       observ.NewMetrics(),
		Tracer:        observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	return service
}

func TestReadyzChecksRegistryAndKeepsUnknownProviderSeparate(t *testing.T) {
	providerStatus := NewAdapterStatusSource(AdapterStatusConfig{Adapters: []AdapterSpec{{Name: "provider", Configured: true}}})
	service := newReadinessTestService(t, readinessRegistrationRepository{}, providerStatus)
	recorder := httptest.NewRecorder()
	service.slice.Server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/readyz status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`"capability":"integration-platform-api"`, `"status":"up"`, `"ready":true`, `"id":"adapter-registry"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("/readyz не содержит %s: %s", want, body)
		}
	}
	if strings.Contains(body, "provider") {
		t.Fatalf("внешние провайдеры не должны смешиваться с API readiness: %s", body)
	}

	statuses, err := providerStatus.Statuses(context.Background(), "")
	if err != nil {
		t.Fatalf("adapter-status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != AdapterStateUnknown || statuses[0].Reason != AdapterReasonProbeNotConfigured {
		t.Fatalf("неподтверждённая внешняя проверка должна оставаться unknown: %+v", statuses)
	}
	statusResponse := httptest.NewRecorder()
	service.slice.Server.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/adapter-status", nil))
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"state":"unknown"`) || !strings.Contains(statusResponse.Body.String(), `"reason":"probe_not_configured"`) {
		t.Fatalf("внешняя проверка должна сообщаться отдельно через adapter-status: code=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
}

func TestReadinessRolesReportUnimplementedBackgroundRolesUnknown(t *testing.T) {
	service := newReadinessTestService(t, readinessRegistrationRepository{}, nil)
	for _, role := range []composition.Role{composition.RoleIngest, composition.RoleWorker, composition.RoleChains, composition.RoleSchedule} {
		report := service.Readiness(context.Background(), role)
		if report.Status != readiness.StatusUnknown || report.Ready {
			t.Errorf("роль %s readiness = %+v, ожидался unknown/not-ready", role, report)
		}
	}
	api := service.Readiness(context.Background(), composition.RoleAPI)
	if api.Status != readiness.StatusUp || !api.Ready {
		t.Fatalf("роль api должна проверять реестр независимо от provider status: %+v", api)
	}
}

func TestReadyzRegistryFailureIsUnavailableAndReasonIsSafe(t *testing.T) {
	const secret = "dsn-password=do-not-leak"
	service := newReadinessTestService(t, readinessRegistrationRepository{err: errors.New(secret)}, nil)
	recorder := httptest.NewRecorder()
	service.slice.Server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz при отказе реестра = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"down"`) || !strings.Contains(recorder.Body.String(), `"reason":"probe_failed"`) {
		t.Fatalf("ожидался безопасный отчёт о сбое зависимости: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("сырая ошибка/секрет попали в readiness: %s", recorder.Body.String())
	}
}
