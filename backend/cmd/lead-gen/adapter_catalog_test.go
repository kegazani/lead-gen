package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/integration-platform/adapterstatus"
)

// TestIntegrationAdapterCatalogNetwork проверяет, что при заданном адресе
// integration-platform раздел интеграций читает состояние адаптеров по сети
// через контракт adapter-status.
func TestIntegrationAdapterCatalogNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(adapterstatus.Response{
			Contract:      adapterstatus.ContractName,
			SchemaVersion: adapterstatus.SchemaVersion,
			Adapters: []adapterstatus.Status{
				{Adapter: "firecrawl", State: adapterstatus.StateUp},
			},
		})
	}))
	defer server.Close()

	catalog, err := integrationAdapterCatalog(buildOptions{integrationPlatformURL: server.URL})
	if err != nil {
		t.Fatalf("сборка каталожного адаптера: %v", err)
	}
	states, err := catalog.States(context.Background())
	if err != nil {
		t.Fatalf("чтение состояния: %v", err)
	}
	if len(states) != 1 || states[0].Adapter != "firecrawl" || states[0].State != adapterstatus.StateUp {
		t.Fatalf("неожидаемое состояние: %+v", states)
	}
}

// TestIntegrationAdapterCatalogInProcess проверяет, что без адреса раздел
// интеграций читает состояние владельца in-proc (co-location).
func TestIntegrationAdapterCatalogInProcess(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/team/credit-usage" {
			t.Errorf("неожиданный probe-запрос: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"remainingCredits":1,"planCredits":1}}`))
	}))
	defer provider.Close()
	catalog, err := integrationAdapterCatalog(buildOptions{firecrawlKey: "test-key", firecrawlURL: provider.URL})
	if err != nil {
		t.Fatalf("сборка каталожного адаптера: %v", err)
	}
	states, err := catalog.States(context.Background())
	if err != nil {
		t.Fatalf("чтение состояния: %v", err)
	}
	var firecrawl string
	for _, state := range states {
		if state.Adapter == "firecrawl" {
			firecrawl = state.State
		}
	}
	if firecrawl != adapterstatus.StateUp {
		t.Fatalf("успешный account balance GET должен подтверждать endpoint, состояние: %+v", states)
	}
	for _, state := range states {
		if state.Adapter == "firecrawl" && state.Reason != "" {
			t.Fatalf("неожиданная причина успешной проверки firecrawl: %+v", state)
		}
	}
}
