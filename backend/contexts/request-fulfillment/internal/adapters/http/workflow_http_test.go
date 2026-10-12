package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	httpapi "lead-gen/backend/contexts/request-fulfillment/internal/adapters/http"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/readiness"
	platformworkflow "lead-gen/backend/platform/workflow"
)

// stubWorkflow — заглушка портов запуска, чтения состояния и сигналов workflow:
// тест проверяет HTTP-слой, а не Temporal.
type stubWorkflow struct {
	run         ports.WorkflowRun
	startCalls  *int
	startErr    error
	status      ports.WorkflowStatus
	statusErr   error
	statusCalls *int
	signalErr   error
}

// StartFulfillment реализует ports.WorkflowStarter.
func (s stubWorkflow) StartFulfillment(context.Context, ports.StartFulfillment) (ports.WorkflowRun, error) {
	if s.startCalls != nil {
		*s.startCalls++
	}
	return s.run, s.startErr
}

// WorkflowStatus реализует ports.WorkflowStatusReader.
func (s stubWorkflow) WorkflowStatus(context.Context, string) (ports.WorkflowStatus, error) {
	if s.statusCalls != nil {
		*s.statusCalls++
	}
	return s.status, s.statusErr
}

// SignalApprovalDecision реализует ports.WorkflowSignaler.
func (s stubWorkflow) SignalApprovalDecision(context.Context, ports.WorkflowDecision) error {
	return s.signalErr
}

// SignalDeliveryOutcome реализует ports.WorkflowSignaler.
func (s stubWorkflow) SignalDeliveryOutcome(context.Context, ports.DeliveryOutcome) error {
	return s.signalErr
}

// recordingSignaler — сигналер, запоминающий последний сигнал утверждения.
type recordingSignaler struct {
	approved bool
	actor    string
	calls    int
}

// SignalApprovalDecision реализует ports.WorkflowSignaler.
func (r *recordingSignaler) SignalApprovalDecision(_ context.Context, in ports.WorkflowDecision) error {
	r.approved = in.Approved
	r.actor = in.Actor
	r.calls++
	return nil
}

// SignalDeliveryOutcome реализует ports.WorkflowSignaler.
func (r *recordingSignaler) SignalDeliveryOutcome(context.Context, ports.DeliveryOutcome) error {
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

func createWorkflowRequest(t *testing.T, server *httptest.Server) {
	t.Helper()
	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-workflow", "tenant_id": tenantID,
		"actor": "client", "content": "Offer", "target": "IT",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание заявки для workflow = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
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
	createWorkflowRequest(t, server)

	health := do(t, server, http.MethodGet, "/healthz", nil)
	if health.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz = %d", health.StatusCode)
	}
	health.Body.Close()

	view := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil))
	if view["phase"] != "searching" || view["execution"] != "RUNNING" {
		t.Fatalf("состояние workflow = %v", view)
	}
	if view["found"] != float64(7) {
		t.Fatalf("найдено лидов = %v", view["found"])
	}
}

func TestHTTPWorkflowStatusHidesUnknownAndCrossTenantBeforeRuntimeLookup(t *testing.T) {
	calls := 0
	server := newWorkflowServer(t, stubWorkflow{
		status:      ports.WorkflowStatus{RequestID: requestID, Phase: "searching"},
		statusCalls: &calls,
	})
	createWorkflowRequest(t, server)

	unknown := do(t, server, http.MethodGet, "/requests/unknown-request/workflow?tenant_id="+tenantID, nil)
	unknownBody := readBody(t, unknown)
	unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестная заявка = %d: %s", unknown.StatusCode, unknownBody)
	}

	crossTenant := do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id=tenant-other", nil)
	crossTenantBody := readBody(t, crossTenant)
	crossTenant.Body.Close()
	if crossTenant.StatusCode != http.StatusNotFound || crossTenantBody != unknownBody {
		t.Fatalf("cross-tenant ответ отличается от unknown: status=%d body=%q; unknown body=%q", crossTenant.StatusCode, crossTenantBody, unknownBody)
	}
	if calls != 0 {
		t.Fatalf("WorkflowStatus вызван до проверки владельца: %d раз", calls)
	}

	authorized := do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil)
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("заявка владельца = %d: %s", authorized.StatusCode, readBody(t, authorized))
	}
	authorized.Body.Close()
	if calls != 1 {
		t.Fatalf("WorkflowStatus после проверки владельца вызван %d раз, ожидался 1", calls)
	}
}

// TestHTTPWorkflowStatusExposesShortfall проверяет наблюдаемость цели и недобора в
// API: при явной цели они видны, при незаданной — недобор не подменяется нулём.
func TestHTTPWorkflowStatusExposesShortfall(t *testing.T) {
	shortfall := 2
	stub := stubWorkflow{status: ports.WorkflowStatus{
		RequestID: requestID, Phase: "awaiting-approval", Found: 5,
		Target: 7, Shortfall: &shortfall,
	}}
	server := newWorkflowServer(t, stub)
	createWorkflowRequest(t, server)

	view := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil))
	if view["target"] != float64(7) || view["shortfall"] != float64(2) {
		t.Fatalf("цель/недобор в API = %v/%v", view["target"], view["shortfall"])
	}

	absent := newWorkflowServer(t, stubWorkflow{status: ports.WorkflowStatus{RequestID: requestID, Phase: "searching", Found: 5}})
	createWorkflowRequest(t, absent)
	absentView := decodeView(t, do(t, absent, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil))
	if _, ok := absentView["shortfall"]; ok {
		t.Fatalf("недобор показан при незаданной цели: %v", absentView)
	}
}

// TestHTTPWorkflowStart проверяет запуск исполнения через API.
func TestHTTPWorkflowStart(t *testing.T) {
	stub := stubWorkflow{run: ports.WorkflowRun{
		WorkflowID: "fulfillment-" + requestID,
		RunID:      "run-1",
	}}
	server := newWorkflowServer(t, stub)
	createWorkflowRequest(t, server)

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

// newRuntimeServer собирает сервер среза с заданным признаком доступности
// рантайма исполнения.
func newRuntimeServer(t *testing.T, availability *platformworkflow.Availability) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{Runtime: availability})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// TestHTTPReadyzTracksRuntime проверяет, что /readyz отражает доступность
// рантайма, а /healthz остаётся пробой живости.
func TestHTTPReadyzTracksRuntime(t *testing.T) {
	availability := platformworkflow.NewAvailability()
	server := newRuntimeServer(t, availability)

	ready := do(t, server, http.MethodGet, "/readyz", nil)
	if ready.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz при недоступном рантайме = %d, ожидался 503", ready.StatusCode)
	}
	ready.Body.Close()

	health := do(t, server, http.MethodGet, "/healthz", nil)
	if health.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz = %d, ожидался 200", health.StatusCode)
	}
	health.Body.Close()

	availability.MarkReady()
	ready = do(t, server, http.MethodGet, "/readyz", nil)
	if ready.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz без PostgreSQL pool и worker = %d, ожидался 503", ready.StatusCode)
	}
	ready.Body.Close()
}

func TestReadyzUsesSafeOwnerProfileAndLeavesLivenessIndependent(t *testing.T) {
	secret := "postgres://user:private@db.internal/tenant?token=credential"
	server := httptest.NewServer(httpapi.NewServer(httpapi.Dependencies{
		Readiness: readiness.Profile{Capability: "request-fulfillment-api", Checks: []readiness.CheckSpec{{
			ID: "postgres", Key: "request-fulfillment/postgres", Required: true,
			Probe: func(context.Context) (readiness.ProbeResult, error) {
				return readiness.ProbeResult{}, fmt.Errorf("dial %s", secret)
			},
		}}},
		ReadinessEvaluator: readiness.New(readiness.Config{Timeout: time.Second, TTL: time.Second}),
	}))
	defer server.Close()

	ready := do(t, server, http.MethodGet, "/readyz", nil)
	body := readBody(t, ready)
	ready.Body.Close()
	if ready.StatusCode != http.StatusServiceUnavailable || strings.Contains(body, secret) || strings.Contains(body, "private") || !strings.Contains(body, "probe_failed") {
		t.Fatalf("/readyz leaked probe data or reported a wrong state: status=%d body=%s", ready.StatusCode, body)
	}

	health := do(t, server, http.MethodGet, "/healthz", nil)
	if health.StatusCode != http.StatusOK {
		t.Fatalf("/healthz должен оставаться независимым от readiness: %d", health.StatusCode)
	}
	health.Body.Close()
}

// TestHTTPWorkflowStartRejectsNegativeTarget проверяет, что отрицательная цель
// запуска отвергается 400 и не доходит до workflow (согласовано с экраном).
func TestHTTPWorkflowStartRejectsNegativeTarget(t *testing.T) {
	stub := stubWorkflow{run: ports.WorkflowRun{
		WorkflowID: "fulfillment-" + requestID,
		RunID:      "run-1",
	}}
	server := newWorkflowServer(t, stub)

	resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/workflow",
		map[string]any{"tenant_id": tenantID, "lead_target": -1})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("отрицательная цель = %d, ожидался 400: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// TestHTTPWorkflowStartAlreadyStarted проверяет, что повторный старт исполнения
// отвечает явным исходом «уже запущено» (200), а не 503.
func TestHTTPWorkflowStartAlreadyStarted(t *testing.T) {
	stub := stubWorkflow{run: ports.WorkflowRun{
		WorkflowID:     "fulfillment-" + requestID,
		RunID:          "run-existing",
		AlreadyStarted: true,
	}}
	server := newWorkflowServer(t, stub)
	createWorkflowRequest(t, server)

	resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/workflow",
		map[string]any{"tenant_id": tenantID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повторный старт = %d, ожидался 200: %s", resp.StatusCode, readBody(t, resp))
	}
	view := decodeView(t, resp)
	if view["already_started"] != true || view["run_id"] != "run-existing" {
		t.Fatalf("ответ повторного старта = %v", view)
	}
}

// TestHTTPWorkflowStatusNoLeads проверяет, что исход «0 лидов» наблюдаем в API.
func TestHTTPWorkflowStatusNoLeads(t *testing.T) {
	stub := stubWorkflow{status: ports.WorkflowStatus{
		RequestID:  requestID,
		WorkflowID: "fulfillment-" + requestID,
		Phase:      "no-leads",
		Found:      0,
		NoLeads:    true,
	}}
	server := newWorkflowServer(t, stub)
	createWorkflowRequest(t, server)

	view := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil))
	if view["no_leads"] != true {
		t.Fatalf("признак no_leads = %v, ожидался true", view["no_leads"])
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

	createWorkflowRequest(t, server)
	unknown := do(t, server, http.MethodGet, "/requests/unknown-request/workflow?tenant_id="+tenantID, nil)
	unknownBody := readBody(t, unknown)
	unknown.Body.Close()
	crossTenant := do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id=tenant-other", nil)
	crossTenantBody := readBody(t, crossTenant)
	crossTenant.Body.Close()
	if unknown.StatusCode != http.StatusNotFound || crossTenant.StatusCode != http.StatusNotFound || crossTenantBody != unknownBody {
		t.Fatalf("без рантайма unknown/cross-tenant различаются: unknown=%d %q, cross=%d %q", unknown.StatusCode, unknownBody, crossTenant.StatusCode, crossTenantBody)
	}
	resp := do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil)
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
	createWorkflowRequest(t, server)

	resp := do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil)
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
		ConsoleUser:      consoleUser,
		ConsolePassword:  consolePassword,
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
	createWorkflowRequest(t, server)
	path := "/requests/" + requestID + "/workflow/approval-decision"
	anonymousRequest, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(`{"tenant_id":"`+tenantID+`","approved":true,"actor":"forged-actor"}`))
	if err != nil {
		t.Fatal(err)
	}
	anonymousRequest.Header.Set("Content-Type", "application/json")
	anonymousResponse, err := noRedirectClient().Do(anonymousRequest)
	if err != nil {
		t.Fatal(err)
	}
	if anonymousResponse.StatusCode != http.StatusUnauthorized || rec.calls != 0 {
		t.Fatalf("anonymous approval decision status=%d calls=%d", anonymousResponse.StatusCode, rec.calls)
	}
	anonymousResponse.Body.Close()

	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())
	resp := client.postForm(t, path, url.Values{"tenant_id": {tenantID}, "approved": {"false"}})
	if resp.StatusCode != http.StatusForbidden || rec.calls != 0 {
		t.Fatalf("approval decision without CSRF status=%d calls=%d", resp.StatusCode, rec.calls)
	}
	resp.Body.Close()

	for _, token := range []string{"", "invalid-csrf-token"} {
		jsonRequest, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(`{"tenant_id":"`+tenantID+`","approved":true,"actor":"forged-actor"}`))
		if err != nil {
			t.Fatal(err)
		}
		jsonRequest.Header.Set("Content-Type", "application/json")
		if token != "" {
			jsonRequest.Header.Set("X-CSRF-Token", token)
		}
		jsonRequest.AddCookie(&http.Cookie{Name: consoleCookie, Value: client.session})
		jsonResponse, err := noRedirectClient().Do(jsonRequest)
		if err != nil {
			t.Fatal(err)
		}
		if jsonResponse.StatusCode != http.StatusForbidden || rec.calls != 0 {
			t.Fatalf("JSON approval decision with CSRF token %q: status=%d calls=%d", token, jsonResponse.StatusCode, rec.calls)
		}
		jsonResponse.Body.Close()
	}

	wrongTenant := client.postForm(t, path, url.Values{"tenant_id": {"tenant-other"}, "csrf": {csrf}, "approved": {"false"}})
	wrongTenantBody := readBody(t, wrongTenant)
	if wrongTenant.StatusCode != http.StatusNotFound || rec.calls != 0 {
		t.Fatalf("cross-tenant decision status=%d calls=%d body=%q", wrongTenant.StatusCode, rec.calls, wrongTenantBody)
	}
	wrongTenant.Body.Close()
	unknown := client.postForm(t, "/requests/unknown-request/workflow/approval-decision", url.Values{"tenant_id": {tenantID}, "csrf": {csrf}, "approved": {"false"}})
	unknownBody := readBody(t, unknown)
	if unknown.StatusCode != http.StatusNotFound || unknownBody != wrongTenantBody || rec.calls != 0 {
		t.Fatalf("unknown/cross-tenant differ: unknown=%d %q cross=%d %q signals=%d", unknown.StatusCode, unknownBody, wrongTenant.StatusCode, wrongTenantBody, rec.calls)
	}
	unknown.Body.Close()

	resp = client.postForm(t, path, url.Values{"tenant_id": {tenantID}, "csrf": {csrf}, "approved": {"false"}, "actor": {"forged-actor"}})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("approval-decision = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if rec.approved || rec.actor != consoleUser || rec.calls != 1 {
		t.Fatalf("сигнал утверждения должен использовать session actor: %+v", rec)
	}
	jsonRequest, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(`{"tenant_id":"`+tenantID+`","approved":true,"actor":"forged-actor"}`))
	if err != nil {
		t.Fatal(err)
	}
	jsonRequest.Header.Set("Content-Type", "application/json")
	jsonRequest.Header.Set("X-CSRF-Token", csrf)
	jsonRequest.AddCookie(&http.Cookie{Name: consoleCookie, Value: client.session})
	jsonResponse, err := noRedirectClient().Do(jsonRequest)
	if err != nil {
		t.Fatal(err)
	}
	if jsonResponse.StatusCode != http.StatusAccepted || !rec.approved || rec.actor != consoleUser || rec.calls != 2 {
		t.Fatalf("authenticated JSON decision status=%d signal=%+v", jsonResponse.StatusCode, rec)
	}
	jsonResponse.Body.Close()
}

// TestHTTPWorkflowSignalUnavailable проверяет честный 503 без рантайма.
func TestHTTPWorkflowSignalUnavailable(t *testing.T) {
	server := newSignalServer(t, nil)
	createWorkflowRequest(t, server)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())
	resp := client.postForm(t, "/requests/"+requestID+"/workflow/approval-decision", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "approved": {"true"},
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("сигнал без рантайма = %d, ожидался 503", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestHTTPWorkflowSignalNotFound проверяет отображение отсутствующего исполнения.
func TestHTTPWorkflowSignalNotFound(t *testing.T) {
	server := newSignalServer(t, stubWorkflow{signalErr: ports.ErrWorkflowNotFound})
	createWorkflowRequest(t, server)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())
	resp := client.postForm(t, "/requests/"+requestID+"/workflow/approval-decision", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "approved": {"true"},
	})
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
