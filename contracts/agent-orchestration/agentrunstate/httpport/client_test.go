package httpport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/agent-orchestration/agentrunstate"
	"lead-gen/contracts/agent-orchestration/agentrunstate/httpport"
)

// ownerStub — двойник владельца: отдаёт фиксированные ответы и заголовки путей,
// чтобы проверить перевод транспорта в исходы контракта.
func ownerStub(t *testing.T, handler http.HandlerFunc) (*httpport.Client, *[]*http.Request) {
	t.Helper()
	requests := make([]*http.Request, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(context.Background()))
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := httpport.New(server.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client, &requests
}

func TestClientRunState(t *testing.T) {
	client, requests := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run_id":"run-1","tenant_id":"tenant-1","status":"running",` +
			`"current_step":"search","cursor":0,"steps":[{"step_id":"search","block_ref":"search","outcome":"unavailable"}]}`))
	})
	state, err := client.RunState(context.Background(), agentrunstate.Request{RunID: "run-1", TenantID: "tenant-1", Actor: "operator"})
	if err != nil {
		t.Fatalf("RunState: %v", err)
	}
	if state.RunID != "run-1" || state.Status != "running" || state.CurrentStep != "search" {
		t.Fatalf("состояние = %+v", state)
	}
	if len(state.Steps) != 1 || state.Steps[0].Outcome != "unavailable" {
		t.Fatalf("шаги = %+v", state.Steps)
	}
	got := (*requests)[0]
	if got.URL.Path != "/agent-runs/run-1" || got.URL.Query().Get("tenant_id") != "tenant-1" || got.URL.Query().Get("actor") != "operator" {
		t.Fatalf("запрос = %s %s", got.URL.Path, got.URL.RawQuery)
	}
}

func TestClientList(t *testing.T) {
	client, requests := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"runs":[{"run_id":"run-1","tenant_id":"tenant-1","status":"running","company_id":"company-1"},` +
			`{"run_id":"run-2","tenant_id":"tenant-1","status":"completed"}],"count":2}`))
	})
	summaries, err := client.List(context.Background(), agentrunstate.ListRequest{TenantID: "tenant-1", Status: "running", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 2 || summaries[0].RunID != "run-1" || summaries[1].Status != "completed" {
		t.Fatalf("список = %+v", summaries)
	}
	got := (*requests)[0]
	if got.URL.Path != "/agent-runs" || got.URL.Query().Get("status") != "running" || got.URL.Query().Get("limit") != "10" {
		t.Fatalf("запрос = %s %s", got.URL.Path, got.URL.RawQuery)
	}
}

func TestClientListEmpty(t *testing.T) {
	client, _ := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0}`))
	})
	summaries, err := client.List(context.Background(), agentrunstate.ListRequest{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 0 || summaries == nil {
		t.Fatalf("пустой список = %+v", summaries)
	}
}

func TestClientErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"не найдено", http.StatusNotFound, agentrunstate.ErrNotFound},
		{"доступ запрещён", http.StatusForbidden, agentrunstate.ErrForbidden},
		{"отклонено", http.StatusBadRequest, agentrunstate.ErrRejected},
		{"недоступно", http.StatusServiceUnavailable, agentrunstate.ErrUnavailable},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, _ := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(`{"error":"причина"}`))
			})
			_, err := client.RunState(context.Background(), agentrunstate.Request{RunID: "run-1", TenantID: "tenant-1"})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, testCase.want)
			}
		})
	}
}

func TestClientRejectsInvalidRequestLocally(t *testing.T) {
	client, _ := ownerStub(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("владелец не должен вызываться при некорректном запросе")
	})
	if _, err := client.List(context.Background(), agentrunstate.ListRequest{}); !errors.Is(err, agentrunstate.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	if _, err := httpport.New(""); err == nil {
		t.Fatal("пустой адрес должен быть отвергнут")
	}
	if _, err := httpport.New("://bad"); err == nil {
		t.Fatal("некорректный адрес должен быть отвергнут")
	}
	if _, err := httpport.New("ftp://host"); err == nil {
		t.Fatal("не-http адрес должен быть отвергнут")
	}
}
