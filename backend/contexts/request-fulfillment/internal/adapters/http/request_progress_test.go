package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

func TestRequestListKeepsRequestAcrossLifecycle(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)
	stages := []struct{ path, status string }{
		{"", "submitted"},
		{"validation/start", "validating"},
		{"validation/complete", "validated"},
		{"processing/start", "processing"},
		{"fulfillment", "fulfilled"},
	}
	for _, stage := range stages {
		if stage.path != "" {
			response := do(t, server, http.MethodPost, "/requests/"+requestID+"/"+stage.path, transition(tenantID))
			if response.StatusCode != http.StatusOK {
				t.Fatalf("переход %s: %d %s", stage.path, response.StatusCode, readBody(t, response))
			}
			response.Body.Close()
		}
		body := readBody(t, client.get(t, "/validation?tenant_id="+tenantID))
		if !strings.Contains(body, `data-request-id="`+requestID+`"`) || !strings.Contains(body, "status-"+stage.status) {
			t.Fatalf("заявка исчезла на стадии %s: %s", stage.status, body)
		}
		if stage.status == "validated" || stage.status == "processing" || stage.status == "fulfilled" {
			if !strings.Contains(body, "/requests/"+requestID+"/approval?tenant_id="+tenantID) {
				t.Fatalf("нет ссылки на исполнение на стадии %s", stage.status)
			}
		}
	}
	other := readBody(t, client.get(t, "/validation?tenant_id=tenant-2"))
	if strings.Contains(other, requestID) {
		t.Fatal("список показал заявку чужого tenant")
	}
}

func TestRequestListShowsSearchProgress(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: ports.WorkflowStatus{
		Phase: "searching", Execution: "Running", Found: 7, Target: 25, Drafted: 0,
	}}, stubWorkflow{})
	client := prepareApprovalClient(t, server)
	body := readBody(t, client.get(t, "/validation?tenant_id="+tenantID))
	for _, want := range []string{requestID, "Поиск", "Выполняется", "<td>7 / 25</td>", "/static/request_progress.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("список не показал %q: %s", want, body)
		}
	}
	response := client.get(t, "/static/request_progress.js")
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "javascript") {
		t.Fatal("скрипт обновления не доступен браузеру")
	}
	response.Body.Close()
}

func TestRequestListKeepsRequestWhenWorkflowStateMissing(t *testing.T) {
	for _, stateErr := range []error{ports.ErrWorkflowNotFound, ports.ErrWorkflowUnavailable} {
		t.Run(stateErr.Error(), func(t *testing.T) {
			server := newApprovalServer(t, stubWorkflow{statusErr: stateErr}, stubWorkflow{})
			client := prepareApprovalClient(t, server)
			body := readBody(t, client.get(t, "/validation?tenant_id="+tenantID))
			if !strings.Contains(body, `data-request-id="`+requestID+`"`) {
				t.Fatal("заявка исчезла при недоступном состоянии")
			}
			if strings.Contains(body, "<td>0</td>") {
				t.Fatal("отсутствующие счётчики подменены нулями")
			}
			want := "Состояние исполнения ещё не получено"
			if stateErr == ports.ErrWorkflowUnavailable {
				want = "недоступ"
			}
			if !strings.Contains(body, want) {
				t.Fatalf("причина отсутствия прогресса не видна: %s", body)
			}
		})
	}
}
