package integrationplatform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirecrawlProbeUsesReadOnlyCreditBalanceEndpoint(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodGet || r.URL.Path != "/v2/team/credit-usage" {
			t.Errorf("probe sent unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("actual provider credential was not used")
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"remainingCredits":25,"planCredits":50}}`))
	}))
	defer server.Close()

	if err := NewFirecrawlProbe(FirecrawlConfig{APIKey: "test-key", BaseURL: server.URL})(context.Background()); err != nil {
		t.Fatalf("Firecrawl Probe: %v", err)
	}
	if !called {
		t.Fatal("provider endpoint was not called")
	}
}

func TestLLMProbeUsesModelsGetAndCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("probe sent unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer llm-test-key" {
			t.Errorf("actual provider credential was not used")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "model-test"}}})
	}))
	defer server.Close()

	if err := NewLLMProbe(OpenAIConfig{BaseURL: server.URL + "/v1", APIKey: "llm-test-key"})(context.Background()); err != nil {
		t.Fatalf("LLM Probe: %v", err)
	}
}

func TestAdapterStatusProbeCacheCoalescesFreshResults(t *testing.T) {
	calls := 0
	source := NewAdapterStatusSource(AdapterStatusConfig{Adapters: []AdapterSpec{{
		Name: "probe", Configured: true,
		Probe: func(context.Context) error { calls++; return nil },
	}}})
	for i := 0; i < 2; i++ {
		statuses, err := source.Statuses(context.Background(), "")
		if err != nil || len(statuses) != 1 || statuses[0].State != AdapterStateUp {
			t.Fatalf("Statuses[%d] = %+v, %v", i, statuses, err)
		}
	}
	if calls != 1 {
		t.Fatalf("probe called %d times, want one call within cache TTL", calls)
	}
}
