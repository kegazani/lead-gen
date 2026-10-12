package httpport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/lead-management/recommendationapply"
	"lead-gen/contracts/lead-management/recommendationapply/httpport"
)

func newClient(t *testing.T, server *httptest.Server) *httpport.Client {
	t.Helper()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// TestApplySuccess проверяет успешное применение: метод, путь, тело команды и
// перевод ответа владельца в Receipt.
func TestApplySuccess(t *testing.T) {
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
		_, _ = io.WriteString(w, `{"job_id":"reco-42"}`)
	}))
	defer server.Close()

	receipt, err := newClient(t, server).ApplyRecommendation(context.Background(), recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "c-1", ScopeKind: "industry", ScopeKey: "saas", Reason: "no_signal", Basis: "наблюдений 3",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "разработка", Targets: []string{"saas-агентства"}, Limit: 5},
	})
	if err != nil {
		t.Fatalf("ApplyRecommendation: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/recommendation-apply" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotBody["tenant_id"] != "tenant-1" || gotBody["change_id"] != "c-1" {
		t.Fatalf("тело команды = %+v", gotBody)
	}
	change, _ := gotBody["change"].(map[string]any)
	if change["kind"] != recommendationapply.KindNewSearch || change["criteria"] != "разработка" || change["limit"] != float64(5) {
		t.Fatalf("структурное изменение = %+v", change)
	}
	if receipt.JobID != "reco-42" {
		t.Fatalf("job_id = %q", receipt.JobID)
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
		{"400 отклонение", http.StatusBadRequest, recommendationapply.ErrRejected},
		{"409 конфликт", http.StatusConflict, recommendationapply.ErrUnavailable},
		{"500 сбой", http.StatusInternalServerError, recommendationapply.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"отказ владельца"}`, tc.status)
			}))
			defer server.Close()

			_, err := newClient(t, server).ApplyRecommendation(context.Background(), recommendationapply.Application{
				TenantID: "t1", ChangeID: "c1", Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "x"},
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, tc.want)
			}
		})
	}
}

// TestMalformedSuccessBodyUnavailable проверяет, что неразборчивый успешный ответ
// считается сбоем транспорта.
func TestMalformedSuccessBodyUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{не json`)
	}))
	defer server.Close()

	_, err := newClient(t, server).ApplyRecommendation(context.Background(), recommendationapply.Application{
		TenantID: "t1", ChangeID: "c1", Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "x"},
	})
	if !errors.Is(err, recommendationapply.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNetworkFailureUnavailable проверяет, что недоступный сервис даёт
// ErrUnavailable.
func TestNetworkFailureUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := httpport.New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.ApplyRecommendation(context.Background(), recommendationapply.Application{}); !errors.Is(err, recommendationapply.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "lead-management:8080", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
