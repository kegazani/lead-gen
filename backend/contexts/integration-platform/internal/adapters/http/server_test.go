package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/app"
	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/contracts/integration-platform/adapterstatus"
)

// statusReaderStub — источник состояния для тестов HTTP-поверхности.
type statusReaderStub struct {
	statuses []adapterstatus.Status
	err      error
}

func (s statusReaderStub) AdapterStatuses(context.Context, string) ([]adapterstatus.Status, error) {
	return s.statuses, s.err
}

// registrationRepoStub — реестр регистраций для тестов.
type registrationRepoStub struct {
	registrations []*domain.AdapterRegistration
	err           error
}

func (r registrationRepoStub) Save(context.Context, *domain.AdapterRegistration) error { return nil }

func (r registrationRepoStub) FindByID(context.Context, domain.AdapterID) (*domain.AdapterRegistration, error) {
	return nil, errors.New("не поддерживается")
}

func (r registrationRepoStub) FindByName(context.Context, domain.AdapterName) (*domain.AdapterRegistration, error) {
	return nil, errors.New("не поддерживается")
}

func (r registrationRepoStub) List(context.Context) ([]*domain.AdapterRegistration, error) {
	return r.registrations, r.err
}

// TestAdapterStatusSurfaceReturnsStates проверяет, что поверхность отдаёт
// состояние объявленных адаптеров в форме контракта adapter-status.
func TestAdapterStatusSurfaceReturnsStates(t *testing.T) {
	server := NewServer(Dependencies{
		Statuses: statusReaderStub{statuses: []adapterstatus.Status{
			{Adapter: "firecrawl", State: adapterstatus.StateUp},
			{Adapter: "llm", State: adapterstatus.StateUnknown, Reason: adapterstatus.ReasonMisconfigured},
		}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/adapter-status", nil)
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d: %s", rec.Code, rec.Body.String())
	}
	var response adapterstatus.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if response.Contract != adapterstatus.ContractName {
		t.Fatalf("имя контракта %q, ожидалось %q", response.Contract, adapterstatus.ContractName)
	}
	if len(response.Adapters) != 2 {
		t.Fatalf("ожидалось 2 адаптера, получено %d", len(response.Adapters))
	}
}

// TestAdapterStatusSurfaceUnavailableWhenSourceMissing проверяет, что без
// источника поверхность честно отвечает 503, а не пустым списком.
func TestAdapterStatusSurfaceUnavailableWhenSourceMissing(t *testing.T) {
	server := NewServer(Dependencies{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/adapter-status", nil)
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ожидался 503, получен %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "не подключён") {
		t.Fatalf("тело не сообщает о недоступности: %s", rec.Body.String())
	}
}

// TestAdaptersSurfaceListsRegistry проверяет, что поверхность отдаёт реестр
// зарегистрированных адаптеров с версией manifest и возможностями.
func TestAdaptersSurfaceListsRegistry(t *testing.T) {
	name, err := domain.ParseAdapterName("firecrawl")
	if err != nil {
		t.Fatalf("имя адаптера: %v", err)
	}
	version, err := domain.ParseManifestVersion("1.2.0")
	if err != nil {
		t.Fatalf("версия manifest: %v", err)
	}
	manifest, err := domain.NewManifest(name, version, []domain.CapabilityName{
		domain.CapabilitySearch, domain.CapabilityCrawl,
	})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	registration, err := domain.RegisterAdapter(domain.RegisterAdapterParams{
		ID:           domain.AdapterID("adapter-1"),
		Manifest:     manifest,
		RegisteredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("регистрация: %v", err)
	}

	server := NewServer(Dependencies{
		Registrations: app.NewListAdaptersHandler(registrationRepoStub{registrations: []*domain.AdapterRegistration{registration}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/adapters", nil)
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d: %s", rec.Code, rec.Body.String())
	}
	var body adaptersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if len(body.Adapters) != 1 {
		t.Fatalf("ожидался 1 адаптер, получено %d", len(body.Adapters))
	}
	if body.Adapters[0].Adapter != "firecrawl" || body.Adapters[0].Version != "1.2.0" {
		t.Fatalf("неожидаемый элемент реестра: %+v", body.Adapters[0])
	}
	if len(body.Adapters[0].Capabilities) != 2 {
		t.Fatalf("ожидалось 2 возможности, получено %d", len(body.Adapters[0].Capabilities))
	}
}

// TestHealthz проверяет проверку живости.
func TestHealthz(t *testing.T) {
	server := NewServer(Dependencies{})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d", rec.Code)
	}
}
