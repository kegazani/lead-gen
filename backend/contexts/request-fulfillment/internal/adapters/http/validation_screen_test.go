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

// newScreenServer собирает сервер среза с экраном валидации и поданной заявкой,
// без подключённого рантайма workflow.
func newScreenServer(t *testing.T) *httptest.Server {
	return newScreenServerWithStarter(t, nil)
}

// newScreenServerWithStarter собирает сервер экрана валидации с подключённым
// портом запуска workflow (nil — рантайм не подключён) и поданной заявкой.
func newScreenServerWithStarter(t *testing.T, starter ports.WorkflowStarter) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		WorkflowStarter: starter,
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
	return server
}

// recordingStarter — заглушка ports.WorkflowStarter, запоминающая запуски.
type recordingStarter struct {
	err     error
	started []ports.StartFulfillment
}

// StartFulfillment реализует ports.WorkflowStarter.
func (r *recordingStarter) StartFulfillment(_ context.Context, in ports.StartFulfillment) (ports.WorkflowRun, error) {
	r.started = append(r.started, in)
	if r.err != nil {
		return ports.WorkflowRun{}, r.err
	}
	return ports.WorkflowRun{WorkflowID: "fulfillment-" + in.RequestID, RunID: "run-1"}, nil
}

// validationScreenPath — адрес экрана валидации заявки.
func validationScreenPath() string {
	return "/requests/" + requestID + "/validation?tenant_id=" + tenantID
}

// TestValidationQueueScreen проверяет очередь валидации: поданная заявка видна на
// экране, а чужой тенант её не видит.
func TestValidationQueueScreen(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)

	body := readBody(t, client.get(t, "/validation?tenant_id="+tenantID))
	if !strings.Contains(body, "Предлагаем автоматизацию найма") {
		t.Fatalf("очередь валидации не показала заявку: %s", body)
	}
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatalf("ответ не является полной HTML-страницей: %s", body)
	}

	other := readBody(t, client.get(t, "/validation?tenant_id=tenant-2"))
	if strings.Contains(other, "Предлагаем автоматизацию найма") {
		t.Fatal("очередь показала заявку чужого тенанта")
	}
}

// TestValidationScreenLifecycle проверяет экран валидации целиком: взятие в
// работу, правку оффера, одобрение.
func TestValidationScreenLifecycle(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)

	// Экран просит взять заявку в работу.
	screen := readBody(t, client.get(t, validationScreenPath()))
	if !strings.Contains(screen, "Взять в работу") {
		t.Fatalf("экран не предложил взять заявку в работу: %s", screen)
	}
	csrf := csrfFromHTML(screen)
	if csrf == "" {
		t.Fatalf("экран не содержит CSRF-токена: %s", screen)
	}

	// Взятие в работу: POST/redirect/GET.
	resp := client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("взятие в работу = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// Правка оффера на месте.
	resp = client.postForm(t, "/requests/"+requestID+"/validation/offer", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
		"content": {"Уточнённый оффер"}, "target": {"SaaS-компании"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("правка оффера = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	screen = readBody(t, client.get(t, validationScreenPath()))
	if !strings.Contains(screen, "Уточнённый оффер") || !strings.Contains(screen, "Одобрить") {
		t.Fatalf("экран не отразил правку и решения: %s", screen)
	}

	// Одобрение.
	resp = client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("одобрение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	screen = readBody(t, client.get(t, validationScreenPath()))
	if !strings.Contains(screen, "Решение валидации") || !strings.Contains(screen, "approved") {
		t.Fatalf("экран не показал решение валидации: %s", screen)
	}
}

// TestValidationRejectRequiresReason проверяет, что экран не пропускает отклонение
// без причины и показывает ошибку.
func TestValidationRejectRequiresReason(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())

	client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	}).Body.Close()

	resp := client.postForm(t, "/requests/"+requestID+"/validation/reject", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("отклонение без причины = %d, ожидался 400: %s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "причина") {
		t.Fatalf("экран не сообщил о необходимости причины: %s", body)
	}
}

// TestValidationCallUnavailable проверяет, что сбой контракта звонка виден на
// экране и не выдаётся за успех.
func TestValidationCallUnavailable(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())

	client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	}).Body.Close()

	resp := client.postForm(t, "/requests/"+requestID+"/validation/call", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("звонок без провайдера = %d, ожидался 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "недоступен") {
		t.Fatalf("экран не сообщил о недоступности контракта: %s", body)
	}
}

// TestValidationApproveStartsWorkflow проверяет, что одобрение валидации при
// подключённом рантайме запускает durable-исполнение заявки.
func TestValidationApproveStartsWorkflow(t *testing.T) {
	starter := &recordingStarter{}
	server := newScreenServerWithStarter(t, starter)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())

	client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	}).Body.Close()

	resp := client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("одобрение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if len(starter.started) != 1 {
		t.Fatalf("запусков workflow = %d, ожидался 1", len(starter.started))
	}
	started := starter.started[0]
	if started.RequestID != requestID {
		t.Fatalf("request_id запуска = %q, ожидался %q", started.RequestID, requestID)
	}
	if started.TenantID != tenantID {
		t.Fatalf("tenant_id запуска = %q, ожидался %q", started.TenantID, tenantID)
	}
	if started.LeadTarget != 0 {
		t.Fatalf("lead_target запуска = %d, ожидался 0 (дефолт workflow)", started.LeadTarget)
	}
}

// TestValidationApproveWithoutWorkflowRuntime проверяет честную деградацию: при
// nil-порте либо недоступном рантайме одобрение не падает.
func TestValidationApproveWithoutWorkflowRuntime(t *testing.T) {
	t.Run("nil-порт", func(t *testing.T) {
		server := newScreenServer(t)
		client := newConsoleClient(server)
		client.login(t)
		csrf := client.csrfForPage(t, validationScreenPath())
		client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
			"tenant_id": {tenantID}, "csrf": {csrf},
		}).Body.Close()

		resp := client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
			"tenant_id": {tenantID}, "csrf": {csrf},
		})
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("одобрение без рантайма = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	})

	t.Run("ErrWorkflowUnavailable", func(t *testing.T) {
		starter := &recordingStarter{err: ports.ErrWorkflowUnavailable}
		server := newScreenServerWithStarter(t, starter)
		client := newConsoleClient(server)
		client.login(t)
		csrf := client.csrfForPage(t, validationScreenPath())
		client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
			"tenant_id": {tenantID}, "csrf": {csrf},
		}).Body.Close()

		resp := client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
			"tenant_id": {tenantID}, "csrf": {csrf},
		})
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("одобрение при недоступном рантайме = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	})
}
