package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// stubWorkflow — заглушка портов запуска, чтения состояния и сигналов workflow:
// тест проверяет HTTP-слой, а не Temporal.
type stubWorkflow struct {
	run       ports.WorkflowRun
	startErr  error
	status    ports.WorkflowStatus
	statusErr error
	signalErr error
}

// StartFulfillment реализует ports.WorkflowStarter.
func (s stubWorkflow) StartFulfillment(context.Context, ports.StartFulfillment) (ports.WorkflowRun, error) {
	return s.run, s.startErr
}

// WorkflowStatus реализует ports.WorkflowStatusReader.
func (s stubWorkflow) WorkflowStatus(context.Context, string) (ports.WorkflowStatus, error) {
	return s.status, s.statusErr
}

// SignalApprovalDecision реализует ports.WorkflowSignaler.
func (s stubWorkflow) SignalApprovalDecision(context.Context, ports.WorkflowDecision) error {
	return s.signalErr
}

// recordingSignaler — сигналер, запоминающий последний сигнал утверждения.
type recordingSignaler struct {
	approved bool
	actor    string
}

// SignalApprovalDecision реализует ports.WorkflowSignaler.
func (r *recordingSignaler) SignalApprovalDecision(_ context.Context, in ports.WorkflowDecision) error {
	r.approved = in.Approved
	r.actor = in.Actor
	return nil
}

// newWorkflowServer собирает сервер среза с заданной заглушкой портов workflow.
func newWorkflowServer(t *testing.T, w stubWorkflow) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		WorkflowStarter:  w,
		WorkflowStatus:   w,
		WorkflowSignaler: w,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// TestHTTPWorkflowStatus проверяет, что состояние исполнения видно в API, а
// health-эндпоинт отвечает.
func TestHTTPWorkflowStatus(t *testing.T) {
	stub := stubWorkflow{status: ports.WorkflowStatus{
		RequestID:  requestID,
		WorkflowID: "fulfillment-" + requestID,
		RunID:      "run-1",
		Phase:      "searching",
		Execution:  "RUNNING",
		Found:      7,
	}}
	server := newWorkflowServer(t, stub)

	health := do(t, server, http.MethodGet, "/healthz", nil)
	if health.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz = %d", health.StatusCode)
	}
	health.Body.Close()

	view := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow", nil))
	if view["phase"] != "searching" || view["execution"] != "RUNNING" {
		t.Fatalf("состояние workflow = %v", view)
	}
	if view["found"] != float64(7) {
		t.Fatalf("найдено лидов = %v", view["found"])
	}
}

// TestHTTPWorkflowStart проверяет запуск исполнения через API.
func TestHTTPWorkflowStart(t *testing.T) {
	stub := stubWorkflow{run: ports.WorkflowRun{
		WorkflowID: "fulfillment-" + requestID,
		RunID:      "run-1",
	}}
	server := newWorkflowServer(t, stub)

	resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/workflow",
		map[string]any{"tenant_id": tenantID, "lead_target": 5})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST workflow = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	view := decodeView(t, resp)
	if view["workflow_id"] != "fulfillment-"+requestID {
		t.Fatalf("workflow_id = %v", view["workflow_id"])
	}
}

// TestHTTPWorkflowUnavailable проверяет, что без подключённого рантайма API
// честно отвечает 503, а не выдумывает состояние.
func TestHTTPWorkflowUnavailable(t *testing.T) {
	slice, err := requestfulfillment.Build(requestfulfillment.Config{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)

	resp := do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET workflow без рантайма = %d, ожидался 503", resp.StatusCode)
	}
	resp.Body.Close()

	resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/workflow", map[string]any{"tenant_id": tenantID})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST workflow без рантайма = %d, ожидался 503", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestHTTPWorkflowNotFound проверяет отображение «исполнение не найдено» в 404.
func TestHTTPWorkflowNotFound(t *testing.T) {
	stub := stubWorkflow{statusErr: ports.ErrWorkflowNotFound}
	server := newWorkflowServer(t, stub)

	resp := do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET workflow = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// newSignalServer собирает сервер среза с заданным портом сигналов workflow.
func newSignalServer(t *testing.T, signaler ports.WorkflowSignaler) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		WorkflowStarter:  stubWorkflow{},
		WorkflowStatus:   stubWorkflow{},
		WorkflowSignaler: signaler,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// TestHTTPWorkflowSignalDecision проверяет подачу сигнала утверждения результата
// через API.
func TestHTTPWorkflowSignalDecision(t *testing.T) {
	rec := &recordingSignaler{}
	server := newSignalServer(t, rec)

	resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/workflow/approval-decision",
		map[string]any{"approved": false, "actor": "operator"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("approval-decision = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if rec.approved || rec.actor != "operator" {
		t.Fatalf("сигнал утверждения = %+v", rec)
	}
}

// TestHTTPWorkflowSignalUnavailable проверяет честный 503 без рантайма.
func TestHTTPWorkflowSignalUnavailable(t *testing.T) {
	server := newSignalServer(t, nil)
	resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/workflow/approval-decision",
		map[string]any{"approved": true})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("сигнал без рантайма = %d, ожидался 503", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestHTTPWorkflowSignalNotFound проверяет отображение отсутствующего исполнения.
func TestHTTPWorkflowSignalNotFound(t *testing.T) {
	server := newSignalServer(t, stubWorkflow{signalErr: ports.ErrWorkflowNotFound})
	resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/workflow/approval-decision",
		map[string]any{"approved": true})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("сигнал отсутствующего исполнения = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// Проверка, что заглушка действительно реализует все три порта.
var (
	_ ports.WorkflowStarter      = stubWorkflow{}
	_ ports.WorkflowStatusReader = stubWorkflow{}
	_ ports.WorkflowSignaler     = stubWorkflow{}
)
