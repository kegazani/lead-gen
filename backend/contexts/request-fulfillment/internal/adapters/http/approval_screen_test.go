package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// newApprovalServer собирает сервер среза с заданными портами чтения состояния
// исполнения и подачи решения. Любой из них может быть nil — тогда рантайм не
// подключён, и экран обязан показать честный исход.
func newApprovalServer(t *testing.T, status ports.WorkflowStatusReader, signaler ports.WorkflowSignaler) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		WorkflowStarter:  stubWorkflow{},
		WorkflowStatus:   status,
		WorkflowSignaler: signaler,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// startProcessingRequest доводит заявку до доменного статуса processing:
// submitted → validating → validated → processing.
func startProcessingRequest(t *testing.T, server *httptest.Server, tenant string) {
	t.Helper()
	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenant,
		"actor": "client", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание заявки = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	for _, path := range []string{"validation/start", "validation/complete", "processing/start"} {
		resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/"+path, transition(tenant))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("переход %s = %d: %s", path, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
}

// awaitingStatus — наблюдаемое состояние исполнения на человеческой паузе.
func awaitingStatus(found int) ports.WorkflowStatus {
	return ports.WorkflowStatus{
		RequestID:  requestID,
		WorkflowID: "fulfillment-" + requestID,
		RunID:      "run-1",
		Phase:      "awaiting-approval",
		Execution:  "RUNNING",
		Found:      found,
	}
}

// postApprovalForm отправляет форму решения без следования перенаправлению.
// htmx добавляет заголовок HTMX, чтобы получить частичный ответ.
func postApprovalForm(t *testing.T, server *httptest.Server, path string, form url.Values, htmx bool) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		request.Header.Set("HX-Request", "true")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(request)
	if err != nil {
		t.Fatalf("отправка формы %s: %v", path, err)
	}
	return resp
}

// approvalSignaler — заглушка ports.WorkflowSignaler, запоминающая решения.
type approvalSignaler struct {
	calls []ports.WorkflowDecision
}

// SignalApprovalDecision реализует ports.WorkflowSignaler.
func (s *approvalSignaler) SignalApprovalDecision(_ context.Context, in ports.WorkflowDecision) error {
	s.calls = append(s.calls, in)
	return nil
}

// errorSignaler — заглушка ports.WorkflowSignaler, возвращающая заданную ошибку.
type errorSignaler struct {
	err error
}

// SignalApprovalDecision реализует ports.WorkflowSignaler.
func (s *errorSignaler) SignalApprovalDecision(context.Context, ports.WorkflowDecision) error {
	return s.err
}

// TestApprovalQueueScreen проверяет очередь утверждения: заявка в фазе
// awaiting-approval видна своему тенанту, чужая — нет.
func TestApprovalQueueScreen(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(7)}, stubWorkflow{})
	startProcessingRequest(t, server, tenantID)

	body := readBody(t, do(t, server, http.MethodGet, "/approvals?tenant_id="+tenantID, nil))
	if !strings.Contains(body, "Предлагаем автоматизацию найма") {
		t.Fatalf("очередь утверждения не показала заявку: %s", body)
	}
	if !strings.Contains(body, "Найдено лидов: 7") {
		t.Fatalf("очередь не показала найденное число: %s", body)
	}
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatalf("ответ не является полной HTML-страницей: %s", body)
	}

	other := readBody(t, do(t, server, http.MethodGet, "/approvals?tenant_id=tenant-2", nil))
	if strings.Contains(other, "Предлагаем автоматизацию найма") {
		t.Fatal("очередь показала заявку чужого тенанта")
	}
	if !strings.Contains(other, "ожидающих утверждения, нет") {
		t.Fatalf("чужая очередь не пуста: %s", other)
	}
}

// TestApprovalQueueUnavailable проверяет честную деградацию: без рантайма
// очередь показывает «исполнение недоступно», а не пустую очередь.
func TestApprovalQueueUnavailable(t *testing.T) {
	server := newApprovalServer(t, nil, nil)
	startProcessingRequest(t, server, tenantID)

	body := readBody(t, do(t, server, http.MethodGet, "/approvals?tenant_id="+tenantID, nil))
	if !strings.Contains(body, "недоступно") {
		t.Fatalf("очередь не сообщила о недоступности исполнения: %s", body)
	}
	if strings.Contains(body, "ожидающих утверждения, нет") {
		t.Fatalf("недоступность выдана за пустую очередь: %s", body)
	}
}

// TestApprovalQueueOmitsNonAwaitingPhase проверяет, что заявка в другой фазе
// исполнения в очередь утверждения не попадает.
func TestApprovalQueueOmitsNonAwaitingPhase(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: ports.WorkflowStatus{Phase: "searching"}}, stubWorkflow{})
	startProcessingRequest(t, server, tenantID)

	body := readBody(t, do(t, server, http.MethodGet, "/approvals?tenant_id="+tenantID, nil))
	if strings.Contains(body, "Предлагаем автоматизацию найма") {
		t.Fatalf("очередь показала заявку не в фазе утверждения: %s", body)
	}
	if !strings.Contains(body, "ожидающих утверждения, нет") {
		t.Fatalf("очередь не пуста: %s", body)
	}
}

// TestApprovalScreen проверяет экран заявки: фаза, найденное число и кнопки.
func TestApprovalScreen(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(7)}, stubWorkflow{})
	startProcessingRequest(t, server, tenantID)

	body := readBody(t, do(t, server, http.MethodGet,
		"/requests/"+requestID+"/approval?tenant_id="+tenantID+"&actor=admin", nil))
	for _, want := range []string{"awaiting-approval", "Найдено лидов: 7", "Утвердить", "Отклонить", "Предлагаем автоматизацию найма"} {
		if !strings.Contains(body, want) {
			t.Fatalf("экран утверждения не содержит %q: %s", want, body)
		}
	}
}

// TestApprovalScreenUnavailable проверяет честную деградацию экрана: без
// рантайма экран показывает «исполнение недоступно» и не предлагает решение.
func TestApprovalScreenUnavailable(t *testing.T) {
	server := newApprovalServer(t, nil, nil)
	startProcessingRequest(t, server, tenantID)

	body := readBody(t, do(t, server, http.MethodGet,
		"/requests/"+requestID+"/approval?tenant_id="+tenantID, nil))
	if !strings.Contains(body, "недоступно") {
		t.Fatalf("экран не сообщил о недоступности исполнения: %s", body)
	}
	if strings.Contains(body, "Утвердить") {
		t.Fatalf("экран предложил решение при недоступном исполнении: %s", body)
	}
}

// TestApprovalScreenNotFound проверяет, что чужой тенант и неизвестная заявка
// неотличимы от отсутствующей.
func TestApprovalScreenNotFound(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(1)}, stubWorkflow{})
	startProcessingRequest(t, server, tenantID)

	resp := do(t, server, http.MethodGet, "/requests/unknown/approval?tenant_id="+tenantID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестная заявка = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()

	resp = do(t, server, http.MethodGet, "/requests/"+requestID+"/approval?tenant_id=tenant-2", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("чужой тенант = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestApprovalApprove проверяет утверждение: сигнал несёт решение и актора,
// а ответ — POST/redirect/GET.
func TestApprovalApprove(t *testing.T) {
	signaler := &approvalSignaler{}
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(3)}, signaler)
	startProcessingRequest(t, server, tenantID)

	resp := postApprovalForm(t, server, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "actor": {"admin"},
	}, false)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("утверждение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if len(signaler.calls) != 1 {
		t.Fatalf("сигналов = %d, ожидался 1", len(signaler.calls))
	}
	call := signaler.calls[0]
	if !call.Approved || call.Actor != "admin" || call.RequestID != requestID {
		t.Fatalf("сигнал утверждения = %+v", call)
	}
}

// TestApprovalReject проверяет отклонение: сигнал несёт отказ и актора.
func TestApprovalReject(t *testing.T) {
	signaler := &approvalSignaler{}
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(3)}, signaler)
	startProcessingRequest(t, server, tenantID)

	resp := postApprovalForm(t, server, "/requests/"+requestID+"/approval/reject", url.Values{
		"tenant_id": {tenantID}, "actor": {"admin"},
	}, false)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("отклонение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if len(signaler.calls) != 1 {
		t.Fatalf("сигналов = %d, ожидался 1", len(signaler.calls))
	}
	if signaler.calls[0].Approved {
		t.Fatalf("отклонение подано как утверждение: %+v", signaler.calls[0])
	}
	if signaler.calls[0].Actor != "admin" {
		t.Fatalf("актор отклонения = %q", signaler.calls[0].Actor)
	}
}

// TestApprovalActionUnavailable проверяет честный 503 без рантайма: решение не
// выдаётся за успех.
func TestApprovalActionUnavailable(t *testing.T) {
	server := newApprovalServer(t, nil, nil)
	startProcessingRequest(t, server, tenantID)

	resp := postApprovalForm(t, server, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "actor": {"admin"},
	}, false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("утверждение без рантайма = %d, ожидался 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "недоступно") {
		t.Fatalf("экран не сообщил о недоступности: %s", body)
	}
}

// TestApprovalActionSignalerUnavailable проверяет, что недоступность рантайма от
// сигналера отдаётся как 503, а не как успех.
func TestApprovalActionSignalerUnavailable(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(1)}, &errorSignaler{err: ports.ErrWorkflowUnavailable})
	startProcessingRequest(t, server, tenantID)

	resp := postApprovalForm(t, server, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "actor": {"admin"},
	}, false)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("недоступный рантайм = %d, ожидался 503", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestApprovalActionNotFound проверяет, что отсутствующее исполнение отдаётся
// как 404.
func TestApprovalActionNotFound(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(1)}, &errorSignaler{err: ports.ErrWorkflowNotFound})
	startProcessingRequest(t, server, tenantID)

	resp := postApprovalForm(t, server, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "actor": {"admin"},
	}, false)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("отсутствующее исполнение = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestApprovalHTMXFragment проверяет частичный ответ для HTMX и контракт
// «фрагмент = часть полной страницы».
func TestApprovalHTMXFragment(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(4)}, stubWorkflow{})
	startProcessingRequest(t, server, tenantID)

	resp := postApprovalForm(t, server, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "actor": {"admin"},
	}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTMX-утверждение = %d, ожидался 200: %s", resp.StatusCode, readBody(t, resp))
	}
	fragment := readBody(t, resp)
	if strings.Contains(fragment, "<!DOCTYPE html>") {
		t.Fatalf("HTMX-ответ вернул полную страницу: %s", fragment)
	}
	if !strings.Contains(fragment, `class="approval-status"`) {
		t.Fatalf("HTMX-ответ не содержит фрагмент состояния: %s", fragment)
	}

	full := readBody(t, do(t, server, http.MethodGet,
		"/requests/"+requestID+"/approval?tenant_id="+tenantID, nil))
	if !strings.Contains(full, strings.TrimSpace(fragment)) {
		t.Fatalf("фрагмент не совпадает с частью полной страницы:\nфрагмент: %s\nстраница: %s", fragment, full)
	}
}
