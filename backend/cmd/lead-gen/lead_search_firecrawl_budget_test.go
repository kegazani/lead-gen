package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	integrationplatform "lead-gen/backend/contexts/integration-platform"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/lead-management/leadsearch"
)

type leadSearchBudgetResponse struct {
	Found         int    `json:"found"`
	FromPool      int    `json:"from_pool"`
	Crawled       int    `json:"crawled"`
	PartialReason string `json:"partial_reason"`
}

type leadSearchHTTP struct {
	url     string
	service *leadmanagement.Service
}

// startLeadSearchHTTP поднимает фактически собранную роль api lead-management.
// Provider остаётся контролируемым httptest-сервером; живой Firecrawl не вызывается.
func startLeadSearchHTTP(t *testing.T, providerURL string, maxCredits int) leadSearchHTTP {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("выделить адрес API: %v", err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatalf("освободить адрес API до старта: %v", err)
	}

	logger := observ.NewLoggerWithWriter(io.Discard)
	opts := buildOptions{
		apiAddr: addr, firecrawlKey: "test-only", firecrawlURL: providerURL,
		firecrawlMaxCredits: maxCredits, smtpHost: "127.0.0.1", smtpPort: 2525,
		logger: logger, metrics: observ.NewMetrics(), leadStore: newLeadManagementStore(),
	}
	service, err := buildLeadManagement(opts, observ.NewTracer(leadmanagement.ServiceID), amqp.NewInMemoryBroker(4))
	if err != nil {
		t.Fatalf("buildLeadManagement: %v", err)
	}
	lm, ok := service.(*leadmanagement.Service)
	if !ok {
		t.Fatalf("сервис типа %T, ожидался *leadmanagement.Service", service)
	}
	api, ok := service.RoleRunner(composition.RoleAPI)
	if !ok {
		t.Fatal("lead-management не собрал роль api")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- api.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("остановка API роли: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("API роль не завершилась после отмены")
		}
	})
	endpoint := "http://" + addr
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("API сервер не начал слушать %s: %v", addr, dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return leadSearchHTTP{url: endpoint, service: lm}
}

func leadSearchProvider(t *testing.T, balance int, handler func(http.ResponseWriter, *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var paid atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/team/credit-usage", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"remainingCredits":` + strconv.Itoa(balance) + `}}`))
	})
	for _, path := range []string{"/v1/search", "/v1/crawl", "/v1/scrape"} {
		path := path
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			paid.Add(1)
			handler(w, r)
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &paid
}

func postLeadSearchHTTP(t *testing.T, endpoint, requestID string) leadSearchBudgetResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"request_id": requestID, "tenant_id": "tenant-a", "criteria": "разработка", "limit": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(endpoint+"/lead-search", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /lead-search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /lead-search status=%d", resp.StatusCode)
	}
	var result leadSearchBudgetResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode /lead-search: %v", err)
	}
	return result
}

func TestBuiltLeadSearchHTTPDeniesOverCapBeforeFirecrawl(t *testing.T) {
	provider, paid := leadSearchProvider(t, 100, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected paid call", http.StatusInternalServerError)
	})
	api := startLeadSearchHTTP(t, provider.URL, 1)
	result := postLeadSearchHTTP(t, api.url, "over-cap")
	if result.PartialReason != "search_unavailable" || paid.Load() != 0 {
		t.Fatalf("admission не отказал до сети: result=%+v paid=%d", result, paid.Load())
	}
}

func TestBuiltLeadSearchHTTPRejectsContactlessPageWhenExtractDenied(t *testing.T) {
	provider, paid := leadSearchProvider(t, 100, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/search":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"url":"https://budget.example/about","title":"Budget Co"}]}`))
		case "/v1/crawl":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"url":"https://budget.example/about","markdown":"# Budget Company"}]}`))
		case "/v1/scrape":
			t.Errorf("Guard должен запретить неподтверждённый Extract до сети")
			http.Error(w, "Extract must not reach network", http.StatusInternalServerError)
		}
	})
	api := startLeadSearchHTTP(t, provider.URL, 20)
	result := postLeadSearchHTTP(t, api.url, "known-cost")
	if result.Found != 0 || result.Crawled != 0 || result.PartialReason != "extract_cost_unknown" {
		t.Fatalf("страница без контактов не должна стать лидом при запрещённом Extract: %+v", result)
	}
	if got := paid.Load(); got != 2 {
		t.Fatalf("платных вызовов=%d, ожидались Search+Crawl, Extract запрещён", got)
	}
}

func TestBuiltLeadSearchHTTPZeroCapStillRequiresAccountAdmission(t *testing.T) {
	provider, paid := leadSearchProvider(t, 1, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected paid call", http.StatusInternalServerError)
	})
	api := startLeadSearchHTTP(t, provider.URL, 0)
	result := postLeadSearchHTTP(t, api.url, "zero-cap-low-account")
	if result.PartialReason != "search_unavailable" || paid.Load() != 0 {
		t.Fatalf("нулевой cap обошёл account admission: result=%+v paid=%d", result, paid.Load())
	}
}

func TestBuiltLeadSearchHTTP429RetryGetsSeparateGuardedAttempt(t *testing.T) {
	var attempts atomic.Int32
	provider, paid := leadSearchProvider(t, 100, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/search" && attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	})
	api := startLeadSearchHTTP(t, provider.URL, 2)
	result := postLeadSearchHTTP(t, api.url, "retry-cap")
	if result.PartialReason != "" || attempts.Load() != 2 || paid.Load() != 2 {
		t.Fatalf("429 retry не завершился подтверждённо: result=%+v attempts=%d paid=%d", result, attempts.Load(), paid.Load())
	}
}

func TestBuiltLeadSearchHTTP402StopsOtherRequestScopes(t *testing.T) {
	provider, paid := leadSearchProvider(t, 100, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "payment required", http.StatusPaymentRequired)
	})
	api := startLeadSearchHTTP(t, provider.URL, 20)
	first := postLeadSearchHTTP(t, api.url, "first-402")
	second := postLeadSearchHTTP(t, api.url, "second-402")
	if first.PartialReason != "search_unavailable" || second.PartialReason != "search_unavailable" || paid.Load() != 1 {
		t.Fatalf("402 не остановил общий Guard: first=%+v second=%+v paid=%d", first, second, paid.Load())
	}
}

func TestBuiltLeadSearchHTTPUnknownOutcomeBlocksSameStableScope(t *testing.T) {
	provider, paid := leadSearchProvider(t, 100, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			http.NotFound(w, r)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("httptest writer не поддерживает закрытие соединения")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	api := startLeadSearchHTTP(t, provider.URL, 20)
	first := postLeadSearchHTTP(t, api.url, "unknown-outcome")
	second := postLeadSearchHTTP(t, api.url, "unknown-outcome")
	if first.PartialReason != "search_unavailable" || second.PartialReason != "search_unavailable" || paid.Load() != 1 {
		t.Fatalf("неизвестный исход был повторён: first=%+v second=%+v paid=%d", first, second, paid.Load())
	}
}

func TestLeadSearchInProcessUsesConfiguredGuardAndCap(t *testing.T) {
	provider, paid := leadSearchProvider(t, 100, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected paid call", http.StatusInternalServerError)
	})
	guard, err := integrationplatform.NewFirecrawlGuard(integrationplatform.FirecrawlGuardConfig{
		APIKey: "test-only", BaseURL: provider.URL,
	})
	if err != nil {
		t.Fatalf("NewFirecrawlGuard: %v", err)
	}
	port := leadmanagement.NewLeadSearch(leadmanagement.LeadSearchDeps{
		Repositories: leadmanagement.NewMemoryRepositories(), Budget: budget.Factory(guard),
		MaxCredits: 1, MaxResults: 1,
	})
	result, err := port.Search(context.Background(), leadsearch.Query{
		RequestID: "in-process-cap", TenantID: "tenant-a", Criteria: "разработка", Limit: 1,
	})
	if err != nil {
		t.Fatalf("lead-search in-process: %v", err)
	}
	if result.PartialReason != "search_unavailable" || paid.Load() != 0 {
		t.Fatalf("in-process путь обошёл Guard: result=%+v paid=%d", result, paid.Load())
	}
}

func TestBuiltLeadSearchHTTPCancellationPersistsPagesAndStopsPaidWork(t *testing.T) {
	var paid atomic.Int32
	var statusCalls atomic.Int32
	firstStatus := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/team/credit-usage", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"remainingCredits":100}}`))
	})
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		paid.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"url":"https://partial.example/about","title":"Partial"}]}`))
	})
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, _ *http.Request) {
		paid.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"id":"job-1"}`))
	})
	mux.HandleFunc("/v1/crawl/job-1", func(w http.ResponseWriter, _ *http.Request) {
		if statusCalls.Add(1) == 1 {
			once.Do(func() { close(firstStatus) })
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"status":"processing"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"status":"cancelled","data":[{"url":"https://partial.example/about","markdown":"# Partial Company\n\nОписание разработки.\n\nhello@partial.example","metadata":{"title":"Partial Company","description":"разработка"}}]}`))
	})
	mux.HandleFunc("/v1/scrape", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("после отмены Guard отправил Extract")
		http.Error(w, "Extract must not reach network", http.StatusInternalServerError)
	})
	provider := httptest.NewServer(mux)
	t.Cleanup(provider.Close)
	api := startLeadSearchHTTP(t, provider.URL, 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := `{"request_id":"cancel-partial","tenant_id":"tenant-a","criteria":"разработка","limit":1}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, api.url+"/lead-search", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	result := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-firstStatus:
		cancel()
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("не начался polling Crawl до срока отмены")
	}
	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP запрос не завершился после отмены")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := api.service.DeliveryTargetResolver("tenant-a").ResolveRecipient(context.Background(), "tenant-a", "hello@partial.example")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("страница не сохранена после отмены: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := paid.Load(); got != 2 {
		t.Fatalf("после отмены число платных вызовов=%d, want Search+Crawl create", got)
	}
}
