package httpapi_test

import (
	"context"
	"errors"
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

// prepareApprovalClient создаёт заявку, доводит её до доменного статуса processing
// (submitted → validating → validated → processing) и возвращает авторизованный
// клиент консоли с CSRF-токеном сессии.
func prepareApprovalClient(t *testing.T, server *httptest.Server) *consoleClient {
	t.Helper()
	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"actor": "client", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание заявки = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	client := newConsoleClient(server)
	client.login(t)
	// CSRF-токен постоянен для сессии: снимаем его с экрана валидации, пока заявка
	// submitted и экран показывает форму действия.
	client.csrf = client.csrfForPage(t, validationScreenPath())

	for _, path := range []string{"validation/start", "validation/complete", "processing/start"} {
		resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/"+path, transition(tenantID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("переход %s = %d: %s", path, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
	return client
}

// approvalScreenPath — адрес экрана утверждения заявки.
func approvalScreenPath() string {
	return "/requests/" + requestID + "/approval?tenant_id=" + tenantID
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
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, "/approvals?tenant_id="+tenantID))
	if !strings.Contains(body, "Предлагаем автоматизацию найма") {
		t.Fatalf("очередь утверждения не показала заявку: %s", body)
	}
	if !strings.Contains(body, "Найдено лидов: 7") {
		t.Fatalf("очередь не показала найденное число: %s", body)
	}
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatalf("ответ не является полной HTML-страницей: %s", body)
	}

	other := readBody(t, client.get(t, "/approvals?tenant_id=tenant-2"))
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
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, "/approvals?tenant_id="+tenantID))
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
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, "/approvals?tenant_id="+tenantID))
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
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, approvalScreenPath()))
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
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, approvalScreenPath()))
	if !strings.Contains(body, "недоступно") {
		t.Fatalf("экран не сообщил о недоступности исполнения: %s", body)
	}
	if strings.Contains(body, "Утвердить") {
		t.Fatalf("экран предложил решение при недоступном исполнении: %s", body)
	}
}

// approvalQueueFailure — сбой чтения состояния исполнения, не являющийся его
// недоступностью: очередь обязана отдать его как ошибку, а не как пустую очередь.
var approvalQueueFailure = errors.New("чтение состояния: сбой")

// TestApprovalQueueError проверяет 500-путь очереди утверждения: прочая ошибка
// чтения состояния (недоступность — отдельный исход) отдаётся как честный сбой.
func TestApprovalQueueError(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{statusErr: approvalQueueFailure}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	resp := client.get(t, "/approvals?tenant_id="+tenantID)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("сбой чтения состояния = %d, ожидался 500: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "очередь утверждения недоступна") {
		t.Fatalf("500-ответ не сообщил о сбое очереди: %s", body)
	}
}

// TestApprovalActionRequiresCSRF проверяет отказ в доступе к решению без
// CSRF-токена: изменяющее действие консоли отклоняется, а с верным токеном
// выполняется.
func TestApprovalActionRequiresCSRF(t *testing.T) {
	signaler := &approvalSignaler{}
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(1)}, signaler)
	client := prepareApprovalClient(t, server)
	action := "/requests/" + requestID + "/approval/approve"

	resp := client.postForm(t, action, url.Values{"tenant_id": {tenantID}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST без CSRF = %d, ожидался 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = client.postForm(t, action, url.Values{"tenant_id": {tenantID}, "csrf": {"чужой-токен"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST с чужим CSRF = %d, ожидался 403", resp.StatusCode)
	}
	resp.Body.Close()

	if len(signaler.calls) != 0 {
		t.Fatalf("отклонённые запросы всё равно подали решение: %+v", signaler.calls)
	}

	resp = client.postForm(t, action, url.Values{"tenant_id": {tenantID}, "csrf": {client.csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST с верным CSRF = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if len(signaler.calls) != 1 {
		t.Fatalf("сигналов после верного CSRF = %d, ожидался 1", len(signaler.calls))
	}
}

// TestApprovalScreenNotFound проверяет, что чужой тенант и неизвестная заявка
// неотличимы от отсутствующей.
func TestApprovalScreenNotFound(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(1)}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	resp := client.get(t, "/requests/unknown/approval?tenant_id="+tenantID)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестная заявка = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()

	resp = client.get(t, "/requests/"+requestID+"/approval?tenant_id=tenant-2")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("чужой тенант = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestApprovalApprove проверяет утверждение: сигнал несёт решение и актора из
// сессии, а ответ — POST/redirect/GET.
func TestApprovalApprove(t *testing.T) {
	signaler := &approvalSignaler{}
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(3)}, signaler)
	client := prepareApprovalClient(t, server)

	resp := client.postForm(t, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {client.csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("утверждение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if len(signaler.calls) != 1 {
		t.Fatalf("сигналов = %d, ожидался 1", len(signaler.calls))
	}
	call := signaler.calls[0]
	if !call.Approved || call.Actor != consoleUser || call.RequestID != requestID {
		t.Fatalf("сигнал утверждения = %+v", call)
	}
}

// TestApprovalReject проверяет отклонение: сигнал несёт отказ и актора из сессии.
func TestApprovalReject(t *testing.T) {
	signaler := &approvalSignaler{}
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(3)}, signaler)
	client := prepareApprovalClient(t, server)

	resp := client.postForm(t, "/requests/"+requestID+"/approval/reject", url.Values{
		"tenant_id": {tenantID}, "csrf": {client.csrf},
	})
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
	if signaler.calls[0].Actor != consoleUser {
		t.Fatalf("актор отклонения = %q, ожидался %q", signaler.calls[0].Actor, consoleUser)
	}
}

// TestApprovalActionUnavailable проверяет честный 503 без рантайма: решение не
// выдаётся за успех.
func TestApprovalActionUnavailable(t *testing.T) {
	server := newApprovalServer(t, nil, nil)
	client := prepareApprovalClient(t, server)

	resp := client.postForm(t, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {client.csrf},
	})
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
	client := prepareApprovalClient(t, server)

	resp := client.postForm(t, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {client.csrf},
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("недоступный рантайм = %d, ожидался 503", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestApprovalActionNotFound проверяет, что отсутствующее исполнение отдаётся
// как 404.
func TestApprovalActionNotFound(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(1)}, &errorSignaler{err: ports.ErrWorkflowNotFound})
	client := prepareApprovalClient(t, server)

	resp := client.postForm(t, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {client.csrf},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("отсутствующее исполнение = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestApprovalHTMXFragment проверяет частичный ответ для HTMX и контракт
// «фрагмент = часть полной страницы».
func TestApprovalHTMXFragment(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(4)}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	resp := client.postFormHTMX(t, "/requests/"+requestID+"/approval/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {client.csrf},
	})
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

	full := readBody(t, client.get(t, approvalScreenPath()))
	if !strings.Contains(full, strings.TrimSpace(fragment)) {
		t.Fatalf("фрагмент не совпадает с частью полной страницы:\nфрагмент: %s\nстраница: %s", fragment, full)
	}
}
