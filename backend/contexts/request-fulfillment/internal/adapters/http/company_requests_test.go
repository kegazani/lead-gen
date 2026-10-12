package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// driveToProcessing доводит уже поданную заявку requestID до статуса processing
// через JSON-эндпоинты переходов.
func driveToProcessing(t *testing.T, server *httptest.Server) {
	t.Helper()
	for _, path := range []string{"validation/start", "validation/complete", "processing/start"} {
		resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/"+path, transition(tenantID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("переход %s = %d: %s", path, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
}

// TestCompanyRequestsList проверяет список заявок компании: заявка видна со
// статусом и историей переходов, а чужой тенант её не видит.
func TestCompanyRequestsList(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)

	body := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	if !strings.Contains(body, "Предлагаем автоматизацию найма") {
		t.Fatalf("список не показал заявку: %s", body)
	}
	if !strings.Contains(body, "История переходов") {
		t.Fatalf("список не показал историю переходов: %s", body)
	}
	if !strings.Contains(body, "Создана") {
		t.Fatalf("список не показал статус submitted: %s", body)
	}

	other := readBody(t, client.get(t, "/company?tenant_id=tenant-2"))
	if strings.Contains(other, "Предлагаем автоматизацию найма") {
		t.Fatal("список показал заявку чужого тенанта")
	}
}

// TestCompanyReturnForRevision проверяет экшен «вернуть на доработку»: заявка из
// validating переходит в needs-revision и список показывает новый статус.
func TestCompanyReturnForRevision(t *testing.T) {
	server := newScreenServer(t)
	if resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/validation/start", transition(tenantID)); resp.StatusCode != http.StatusOK {
		t.Fatalf("validation/start = %d: %s", resp.StatusCode, readBody(t, resp))
	} else {
		resp.Body.Close()
	}

	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, "/company?tenant_id="+tenantID)

	resp := client.postForm(t, "/requests/"+requestID+"/revision", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "reason": {"уточните цель"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("возврат на доработку = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	body := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	if !strings.Contains(body, "Нужна доработка") {
		t.Fatalf("список не показал needs-revision: %s", body)
	}
}

// TestCompanyReturnRequiresReason проверяет, что возврат на доработку без
// комментария отклоняется.
func TestCompanyReturnRequiresReason(t *testing.T) {
	server := newScreenServer(t)
	if resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/validation/start", transition(tenantID)); resp.StatusCode != http.StatusOK {
		t.Fatalf("validation/start = %d: %s", resp.StatusCode, readBody(t, resp))
	} else {
		resp.Body.Close()
	}

	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, "/company?tenant_id="+tenantID)

	resp := client.postForm(t, "/requests/"+requestID+"/revision", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("возврат без причины = %d, ожидался 400: %s", resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); !strings.Contains(body, "причина") {
		t.Fatalf("экран не сообщил о необходимости комментария: %s", body)
	}
}

// TestCompanyCancelFromProcessing проверяет отмену заявки в работе: заявка
// переходит в cancelled и список показывает терминальный статус.
func TestCompanyCancelFromProcessing(t *testing.T) {
	server := newScreenServer(t)
	driveToProcessing(t, server)

	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, "/company?tenant_id="+tenantID)

	resp := client.postForm(t, "/requests/"+requestID+"/cancel", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "reason": {"клиент отказался"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("отмена = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	body := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	if !strings.Contains(body, "Отменена") {
		t.Fatalf("список не показал cancelled: %s", body)
	}
}

// TestCompanyRequestsShowsFailedReason проверяет, что упавшая заявка не прячется из
// списка заявок компании: видна с терминальным статусом «Сбой» и причиной сбоя в
// истории переходов, а не как «В работе».
func TestCompanyRequestsShowsFailedReason(t *testing.T) {
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		Clock:           fixedClock{at: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		ConsoleUser:     consoleUser,
		ConsolePassword: consolePassword,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)

	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"actor": "client", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание заявки = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	driveToProcessing(t, server)

	if _, err := slice.FailRequest.Handle(context.Background(), command.FailRequest{
		LifecycleFields: command.LifecycleFields{
			RequestID: domain.RequestID(requestID),
			TenantID:  domain.TenantID(tenantID),
			Actor:     "admin",
		},
		ReasonCode: "search_failed",
		Reason:     "поиск упал",
	}); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}

	client := newConsoleClient(server)
	client.login(t)
	body := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	if !strings.Contains(body, "Сбой") {
		t.Fatalf("список не показал упавшую заявку статусом «Сбой»: %s", body)
	}
	if !strings.Contains(body, "поиск упал") {
		t.Fatalf("список не показал причину сбоя: %s", body)
	}
	if !strings.Contains(body, "status-failed") {
		t.Fatalf("заявка не отображена со статусом failed: %s", body)
	}
	if strings.Contains(body, "status-processing") {
		t.Fatal("упавшая заявка отображена как «В работе»")
	}
}

// TestCompanyRerunFailedRequest проверяет операторскую поверхность повторного
// запуска: кнопка Rerun на экране заявок компании переводит упавшую заявку
// failed → validated и перезапускает исполнение (StartFulfillment).
func TestCompanyRerunFailedRequest(t *testing.T) {
	starter := &recordingStarter{}
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		Clock:           fixedClock{at: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		ConsoleUser:     consoleUser,
		ConsolePassword: consolePassword,
		WorkflowStarter: starter,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)

	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"actor": "client", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание заявки = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	driveToProcessing(t, server)

	if _, err := slice.FailRequest.Handle(context.Background(), command.FailRequest{
		LifecycleFields: command.LifecycleFields{
			RequestID: domain.RequestID(requestID),
			TenantID:  domain.TenantID(tenantID),
			Actor:     "admin",
		},
		ReasonCode: "search_failed",
		Reason:     "поиск упал",
	}); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}

	client := newConsoleClient(server)
	client.login(t)

	// Кнопка Rerun видна у упавшей заявки.
	listBefore := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	if !strings.Contains(listBefore, "Повторить запуск") {
		t.Fatalf("упавшая заявка не показала действие «Повторить запуск»: %s", listBefore)
	}

	csrf := client.csrfForPage(t, "/company?tenant_id="+tenantID)
	resp = client.postForm(t, "/requests/"+requestID+"/rerun", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("повторный запуск = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	body := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	if !strings.Contains(body, "Создана") {
		t.Fatalf("после повторного запуска заявка не перешла в validated: %s", body)
	}
	if len(starter.started) != 1 {
		t.Fatalf("запусков исполнения = %d, ожидался 1", len(starter.started))
	}
	if starter.started[0].RequestID != requestID || starter.started[0].TenantID != tenantID {
		t.Fatalf("запуск исполнения = %+v, ожидалась заявка %s тенанта %s", starter.started[0], requestID, tenantID)
	}
}

func TestV2CompanyRerunDoesNotCallTemporalAndRejectsActivePredecessor(t *testing.T) {
	starts := 0
	starter := &stubWorkflow{startCalls: &starts}
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		ExecutionProtocol: requestfulfillment.ExecutionProtocolV2,
		ExecutionConsumer: activeIntentConsumer(true), ExecutionManifestActive: true,
		WorkflowStarter: starter, ConsoleUser: consoleUser, ConsolePassword: consolePassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"actor": "client", "content": "Offer", "target": "IT",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create request=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/validation/start", transition(tenantID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start validation=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	client := newConsoleClient(server)
	client.login(t)
	screen := readBody(t, client.get(t, validationScreenPath()))
	csrf := csrfFromHTML(screen)
	commandMatch := regexp.MustCompile(`name="command_id" value="([^"]+)"`).FindStringSubmatch(screen)
	if csrf == "" || len(commandMatch) != 2 {
		t.Fatalf("v2 approval form csrf/CommandID missing: %s", screen)
	}
	resp = client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "command_id": {commandMatch[1]},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("v2 approve=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if _, err := slice.StartProcessing.Handle(context.Background(), command.StartProcessing{
		LifecycleFields: command.LifecycleFields{RequestID: domain.RequestID(requestID), TenantID: domain.TenantID(tenantID), Actor: "test"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := slice.FailRequest.Handle(context.Background(), command.FailRequest{
		LifecycleFields: command.LifecycleFields{RequestID: domain.RequestID(requestID), TenantID: domain.TenantID(tenantID), Actor: "test"},
		ReasonCode:      "search_failed", Reason: "test failure",
	}); err != nil {
		t.Fatal(err)
	}
	company := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	if strings.Contains(company, "/requests/"+requestID+"/rerun") {
		t.Fatal("v2 экран не должен предлагать заблокированный rerun")
	}
	before, _ := slice.Journal.Pending(context.Background(), 0)
	resp = client.postForm(t, "/requests/"+requestID+"/rerun", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "command_id": {"new-business-command"},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("v2 rerun with active predecessor=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	after, _ := slice.Journal.Pending(context.Background(), 0)
	if starts != 0 || len(after) != len(before) {
		t.Fatalf("v2 rerun started Temporal or wrote outbox: starts=%d before=%d after=%d", starts, len(before), len(after))
	}
}

// TestCompanyRequestsRenderGolden фиксирует полную разметку списка заявок
// компании с заявкой в работе: разметка не меняется незаметно. Время берётся из
// фиксированных часов, чтобы золотой файл не зависел от системного времени.
func TestCompanyRequestsRenderGolden(t *testing.T) {
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		Clock:           fixedClock{at: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		ConsoleUser:     consoleUser,
		ConsolePassword: consolePassword,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)

	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"actor": "client", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание заявки = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	driveToProcessing(t, server)

	client := newConsoleClient(server)
	client.login(t)

	body := readBody(t, client.get(t, "/company?tenant_id="+tenantID))
	assertApprovalGolden(t, "company-requests.golden", body)
}
