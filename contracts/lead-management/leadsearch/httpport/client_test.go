package httpport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/lead-management/leadsearch/httpport"
)

// newClient собирает адаптер на тестовом сервере.
func newClient(t *testing.T, server *httptest.Server) *httpport.Client {
	t.Helper()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// TestSearchSuccess проверяет успешный поиск: метод, путь, тело запроса и перевод
// ответа владельца в Result.
func TestSearchSuccess(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"found":2,"from_pool":1,"crawled":1,"leads":[{"id":"pool.example","origin":"pool"},{"id":"crawl.example","origin":"crawl"}]}`)
	}))
	defer server.Close()

	result, err := newClient(t, server).Search(context.Background(), leadsearch.Query{
		RequestID: "req-9", TenantID: "tenant-1", Criteria: "разработка", Limit: 2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/lead-search" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotBody["request_id"] != "req-9" || gotBody["tenant_id"] != "tenant-1" ||
		gotBody["criteria"] != "разработка" || gotBody["limit"] != float64(2) {
		t.Fatalf("тело команды = %+v", gotBody)
	}
	want := leadsearch.Result{
		Found:    2,
		FromPool: 1,
		Crawled:  1,
		Leads: []leadsearch.LeadRef{
			{ID: "pool.example", Origin: leadsearch.OriginPool},
			{ID: "crawl.example", Origin: leadsearch.OriginCrawl},
		},
	}
	if result.Found != want.Found || result.FromPool != want.FromPool || result.Crawled != want.Crawled {
		t.Fatalf("результат = %+v, ожидалось %+v", result, want)
	}
	if len(result.Leads) != 2 || result.Leads[0] != want.Leads[0] || result.Leads[1] != want.Leads[1] {
		t.Fatalf("leads = %+v", result.Leads)
	}
}

// TestEmptyLeads проверяет, что пустой список результатов читается без ошибки.
func TestEmptyLeads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"found":0,"from_pool":0,"crawled":0}`)
	}))
	defer server.Close()

	result, err := newClient(t, server).Search(context.Background(), leadsearch.Query{Criteria: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Found != 0 || len(result.Leads) != 0 {
		t.Fatalf("результат = %+v", result)
	}
}

func TestSearchPreservesPartialReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"found":1,"from_pool":0,"crawled":1,"leads":[{"id":"partial.example","origin":"crawl"}],"partial_reason":"extract_cost_unknown"}`)
	}))
	defer server.Close()

	result, err := newClient(t, server).Search(context.Background(), leadsearch.Query{Criteria: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.PartialReason != "extract_cost_unknown" || result.Found != 1 || len(result.Leads) != 1 {
		t.Fatalf("частичная причина потеряна при разборе wire-ответа: %+v", result)
	}
}

// TestErrorMapping проверяет перевод HTTP-кодов владельца в исходы контракта:
// 400 — доменный отказ, прочее — недоступность.
func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"400 отклонение", http.StatusBadRequest, leadsearch.ErrRejected},
		{"409 конфликт", http.StatusConflict, leadsearch.ErrUnavailable},
		{"500 сбой", http.StatusInternalServerError, leadsearch.ErrUnavailable},
		{"503 недоступно", http.StatusServiceUnavailable, leadsearch.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"отказ владельца"}`, tc.status)
			}))
			defer server.Close()

			if _, err := newClient(t, server).Search(context.Background(), leadsearch.Query{Criteria: "x"}); !errors.Is(err, tc.want) {
				t.Fatalf("Search(%d) ошибка = %v, ожидалась %v", tc.status, err, tc.want)
			}
		})
	}
}

// TestMalformedSuccessBodyUnavailable проверяет, что неразборчивый успешный ответ
// считается сбоем транспорта, а не выдуманным исходом.
func TestMalformedSuccessBodyUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{не json`)
	}))
	defer server.Close()

	if _, err := newClient(t, server).Search(context.Background(), leadsearch.Query{Criteria: "x"}); !errors.Is(err, leadsearch.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNetworkFailureUnavailable проверяет, что недоступный сервис даёт
// ErrUnavailable, а не доменный исход.
func TestNetworkFailureUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := httpport.New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Search(context.Background(), leadsearch.Query{Criteria: "x"}); !errors.Is(err, leadsearch.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestContextDeadlineHonored проверяет, что дедлайн берётся из вызова и завершает
// запрос недоступностью, а не висит бесконечно.
func TestContextDeadlineHonored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := newClient(t, server).Search(ctx, leadsearch.Query{Criteria: "x"}); !errors.Is(err, leadsearch.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("дедлайн вызова не соблюдён: %v", elapsed)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера, а не при первом поиске.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "lead-management:8080", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
