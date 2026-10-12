package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/lead-management/leadsearch/httpport"
)

// newLeadSearchClient собирает сервер роли api с поиском лидов под заявку и
// сетевой клиент контракта lead-search к нему (round-trip).
func newLeadSearchClient(t *testing.T, useCase *app.SearchLeadsForRequest) *httpport.Client {
	t.Helper()
	server := httpapi.NewServer(httpapi.Dependencies{SearchLeads: useCase})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client
}

// TestLeadSearchHTTPRoundTrip проверяет сквозной путь контракта: сетевой клиент →
// POST /lead-search → use case → ответ, читаемый клиентом.
func TestLeadSearchHTTPRoundTrip(t *testing.T) {
	profiles := memory.NewLeadProfileRepository()
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain: "pool.example",
		Name: domain.AttributedValue{
			Value:      "Pool Co",
			Provenance: domain.Provenance{SourceURL: "https://pool.example", ExtractedAt: testTime},
		},
		Description: domain.AttributedValue{
			Value:      "разработка ПО",
			Provenance: domain.Provenance{SourceURL: "https://pool.example", ExtractedAt: testTime},
		},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if err := profiles.Save(ports.WithTenant(context.Background(), "tenant-1"), company); err != nil {
		t.Fatalf("Save: %v", err)
	}

	useCase := &app.SearchLeadsForRequest{
		Profiles: profiles,
		Contacts: memory.NewContactRepository(),
		Clock:    fakeClock{t: testTime},
	}
	client := newLeadSearchClient(t, useCase)

	result, err := client.Search(context.Background(), leadsearch.Query{
		RequestID: "req-1", TenantID: "tenant-1", Criteria: "разработка", Limit: 5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Found != 1 || result.FromPool != 1 || result.Crawled != 0 {
		t.Fatalf("результат = %+v, ожидалось Found=1 FromPool=1 Crawled=0", result)
	}
	if len(result.Leads) != 1 || result.Leads[0].ID != "pool.example" || result.Leads[0].Origin != leadsearch.OriginPool {
		t.Fatalf("leads = %+v", result.Leads)
	}
}

// TestLeadSearchHTTPRejectsEmptyCriteria проверяет перевод доменного отказа в 400 и
// ErrRejected на стороне клиента.
func TestLeadSearchHTTPRejectsEmptyCriteria(t *testing.T) {
	useCase := &app.SearchLeadsForRequest{
		Profiles: memory.NewLeadProfileRepository(),
		Contacts: memory.NewContactRepository(),
		Clock:    fakeClock{t: testTime},
	}
	client := newLeadSearchClient(t, useCase)

	if _, err := client.Search(context.Background(), leadsearch.Query{Criteria: "   "}); !errors.Is(err, leadsearch.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestLeadSearchHTTPUnavailable проверяет, что незамонтированный контракт честно
// отвечает недоступностью, а не пустым успехом.
func TestLeadSearchHTTPUnavailable(t *testing.T) {
	client := newLeadSearchClient(t, nil)

	if _, err := client.Search(context.Background(), leadsearch.Query{Criteria: "разработка"}); !errors.Is(err, leadsearch.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
