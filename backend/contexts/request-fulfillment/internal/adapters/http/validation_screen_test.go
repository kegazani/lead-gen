package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
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
	if !strings.Contains(visibleText(body), "Создана") {
		t.Fatalf("очередь валидации не показала статус submitted как «Создана»: %s", visibleText(body))
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
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ошибка старта исполнения = %d, ожидался 503: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	screen = readBody(t, client.get(t, validationScreenPath()))
	if !strings.Contains(screen, "Решение валидации") || !strings.Contains(screen, "Одобрена") {
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
// TestValidationApproveEmptyLeadTarget проверяет обратную совместимость: без
// явной цели (пустое поле или 0) исполнение запускается с LeadTarget 0.
func TestValidationApproveEmptyLeadTarget(t *testing.T) {
	for _, raw := range []string{"", "0"} {
		raw := raw
		t.Run("value="+raw, func(t *testing.T) {
			starter := &recordingStarter{}
			server := newScreenServerWithStarter(t, starter)
			client := newConsoleClient(server)
			client.login(t)
			csrf := client.csrfForPage(t, validationScreenPath())
			client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
				"tenant_id": {tenantID}, "csrf": {csrf},
			}).Body.Close()

			form := url.Values{"tenant_id": {tenantID}, "csrf": {csrf}}
			if raw != "" {
				form.Set("lead_target", raw)
			}
			resp := client.postForm(t, "/requests/"+requestID+"/validation/approve", form)
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("одобрение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
			}
			resp.Body.Close()

			if len(starter.started) != 1 {
				t.Fatalf("запусков workflow = %d, ожидался 1", len(starter.started))
			}
			if started := starter.started[0]; started.LeadTarget != 0 {
				t.Fatalf("lead_target запуска = %d, ожидался 0 (цель не задана)", started.LeadTarget)
			}
		})
	}
}

// TestValidationApprovePassesLeadTarget проверяет, что заданная на экране цель
// по числу лидов пробрасывается в запуск исполнения.
func TestValidationApprovePassesLeadTarget(t *testing.T) {
	starter := &recordingStarter{}
	server := newScreenServerWithStarter(t, starter)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())
	client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	}).Body.Close()

	resp := client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "lead_target": {"7"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("одобрение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if len(starter.started) != 1 {
		t.Fatalf("запусков workflow = %d, ожидался 1", len(starter.started))
	}
	if started := starter.started[0]; started.LeadTarget != 7 {
		t.Fatalf("lead_target запуска = %d, ожидался 7", started.LeadTarget)
	}
}

// TestValidationApproveRejectsNegativeLeadTarget проверяет, что отрицательная
// цель отклоняется понятной ошибкой 400, а одобрение не применяется: заявка
// остаётся validating, исполнение не запускается.
func TestValidationApproveRejectsNegativeLeadTarget(t *testing.T) {
	starter := &recordingStarter{}
	server := newScreenServerWithStarter(t, starter)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())
	client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	}).Body.Close()

	resp := client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "lead_target": {"-3"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("отрицательная цель = %d, ожидался 400: %s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "цель по числу лидов") {
		t.Fatalf("экран не сообщил о некорректной цели: %s", body)
	}
	if len(starter.started) != 0 {
		t.Fatalf("исполнение запущено при некорректной цели: %+v", starter.started)
	}

	// Заявка не перешла в validated: экран по-прежнему предлагает одобрение.
	screen := readBody(t, client.get(t, validationScreenPath()))
	if !strings.Contains(screen, "Одобрить") {
		t.Fatalf("заявка перешла из validating при отклонённой цели: %s", screen)
	}
}

// TestValidationScreenRenderGolden фиксирует полную разметку экрана валидации с
// полем цели по числу лидов: форма одобрения не меняется незаметно.
func TestValidationScreenRenderGolden(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, validationScreenPath())
	client.postForm(t, "/requests/"+requestID+"/validation/take", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf},
	}).Body.Close()

	body := readBody(t, client.get(t, validationScreenPath()))
	assertApprovalGolden(t, "validation-screen.golden", body)
}

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
		if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(readBody(t, resp), ports.ErrWorkflowUnavailable.Error()) {
			t.Fatalf("одобрение без рантайма не показало исходный отказ: %d", resp.StatusCode)
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
		if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(readBody(t, resp), ports.ErrWorkflowUnavailable.Error()) {
			t.Fatalf("одобрение при недоступном рантайме не показало исходный отказ: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})
}

type activeIntentConsumer bool

func (c activeIntentConsumer) Active() bool { return bool(c) }

func TestV2ApprovalCommitsIntentAndNeverStartsTemporal(t *testing.T) {
	starts := 0
	starter := &stubWorkflow{startCalls: &starts}
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		ExecutionProtocol:       requestfulfillment.ExecutionProtocolV2,
		ExecutionConsumer:       activeIntentConsumer(true),
		ExecutionManifestActive: true,
		WorkflowStarter:         starter,
		ConsoleUser:             consoleUser,
		ConsolePassword:         consolePassword,
		WorkflowStatus:          starter,
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
	commandID := regexp.MustCompile(`name="command_id" value="([^"]+)"`).FindStringSubmatch(screen)
	if csrf == "" || len(commandID) != 2 {
		t.Fatalf("v2 approval form missing csrf/CommandID: %s", screen)
	}
	resp = client.postForm(t, "/requests/"+requestID+"/validation/approve", url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "command_id": {commandID[1]}, "lead_target": {"not-a-number"},
		"request_version": {"1"}, "offer_revision": {"1"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid v2 approval=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	retriedScreen := readBody(t, resp)
	resp.Body.Close()
	retriedID := regexp.MustCompile(`name="command_id" value="([^"]+)"`).FindStringSubmatch(retriedScreen)
	if len(retriedID) != 2 || retriedID[1] != commandID[1] {
		t.Fatalf("validation rerender replaced CommandID: initial=%q rerender=%q", commandID[1], retriedID)
	}
	approvalForm := url.Values{
		"tenant_id": {tenantID}, "csrf": {csrf}, "command_id": {retriedID[1]}, "lead_target": {"11"},
		"request_version": {"1"}, "offer_revision": {"1"},
	}
	resp = client.postForm(t, "/requests/"+requestID+"/validation/approve", approvalForm)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("v2 approval=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	pendingAfterCommit, err := slice.Journal.Pending(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	resp = client.postForm(t, "/requests/"+requestID+"/validation/approve", approvalForm)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("ambiguous-commit replay with same CommandID=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	pendingAfterReplay, err := slice.Journal.Pending(context.Background(), 0)
	if err != nil || len(pendingAfterReplay) != len(pendingAfterCommit) {
		t.Fatalf("same HTML command replay added outbox entry: before=%d after=%d err=%v", len(pendingAfterCommit), len(pendingAfterReplay), err)
	}
	if starts != 0 {
		t.Fatalf("HTTP v2 вызвал Temporal starter %d раз", starts)
	}
	pending, err := slice.Journal.Pending(context.Background(), 0)
	if err != nil || len(pending) != 3 {
		t.Fatalf("outbox после atomic admission=%d err=%v (ожидались submit, validation и intent)", len(pending), err)
	}
	stored, err := slice.Repository.FindByID(context.Background(), "req-1")
	if err != nil || stored.Status() != "validated" {
		t.Fatalf("request lifecycle status=%v err=%v", stored.Status(), err)
	}
	result, err := slice.AdmitExecution.Handle(context.Background(), command.AdmitExecution{
		LifecycleFields: command.LifecycleFields{RequestID: "req-1", TenantID: "tenant-1", Actor: "operator"},
		CommandID:       domain.CommandID(commandID[1]), Kind: command.AdmissionApprove, LeadTarget: intPointer(11),
	})
	if err != nil || !result.Replay || result.Generation != 1 {
		t.Fatalf("post-commit replay=%+v err=%v", result, err)
	}
}

func TestV2DirectEndpointRequiresDurableIdempotencyAndDoesNotStartTemporal(t *testing.T) {
	starts := 0
	starter := &stubWorkflow{startCalls: &starts}
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		ExecutionProtocol:       requestfulfillment.ExecutionProtocolV2,
		ExecutionConsumer:       activeIntentConsumer(true),
		ExecutionManifestActive: true,
		WorkflowStarter:         starter,
		ConsoleUser:             consoleUser,
		ConsolePassword:         consolePassword,
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
	resp = do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil)
	initialProjection := decodeView(t, resp)
	if initialProjection["state"] != "not-admitted" || initialProjection["execution_history_known"] != true {
		t.Fatalf("durable query without Temporal runtime = %v", initialProjection)
	}
	resp = do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id=tenant-other", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant durable projection=%d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	console := newConsoleClient(server)
	console.login(t)
	csrfRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/requests/"+requestID+"/workflow", strings.NewReader(`{"tenant_id":"`+tenantID+`"}`))
	csrfRequest.Header.Set("Content-Type", "application/json")
	csrfRequest.AddCookie(&http.Cookie{Name: consoleCookie, Value: console.session})
	csrfResponse, err := http.DefaultClient.Do(csrfRequest)
	if err != nil {
		t.Fatal(err)
	}
	if csrfResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie-authenticated v2 mutation without CSRF=%d", csrfResponse.StatusCode)
	}
	csrfResponse.Body.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/requests/"+requestID+"/workflow", strings.NewReader(`{"tenant_id":"`+tenantID+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("direct v2 without Idempotency-Key=%d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/requests/"+requestID+"/workflow", strings.NewReader(`{"tenant_id":"`+tenantID+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-direct")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unvalidated direct v2 admission=%d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	pending, err := slice.Journal.Pending(context.Background(), 0)
	if err != nil || len(pending) != 1 {
		t.Fatalf("legacy rejection wrote intent/outbox: pending=%d err=%v", len(pending), err)
	}
	for _, path := range []string{"validation/start", "validation/complete"} {
		response := do(t, server, http.MethodPost, "/requests/"+requestID+"/"+path, transition(tenantID))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s=%d: %s", path, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
	pendingBeforeFailures, err := slice.Journal.Pending(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	wrongTenant, _ := http.NewRequest(http.MethodPost, server.URL+"/requests/"+requestID+"/workflow", strings.NewReader(`{"tenant_id":"tenant-other"}`))
	wrongTenant.Header.Set("Content-Type", "application/json")
	wrongTenant.Header.Set("Idempotency-Key", "command-direct")
	wrongResponse, err := http.DefaultClient.Do(wrongTenant)
	if err != nil {
		t.Fatal(err)
	}
	wrongBody := readBody(t, wrongResponse)
	if wrongResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant admission=%d: %s", wrongResponse.StatusCode, wrongBody)
	}
	wrongResponse.Body.Close()
	unknownRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/requests/unknown-request/workflow", strings.NewReader(`{"tenant_id":"tenant-other"}`))
	unknownRequest.Header.Set("Content-Type", "application/json")
	unknownRequest.Header.Set("Idempotency-Key", "command-direct")
	unknownResponse, err := http.DefaultClient.Do(unknownRequest)
	if err != nil {
		t.Fatal(err)
	}
	unknownBody := readBody(t, unknownResponse)
	if unknownResponse.StatusCode != http.StatusNotFound || unknownBody != wrongBody {
		t.Fatalf("unknown request differs from cross-tenant response: status=%d body=%q, cross-tenant status=%d body=%q", unknownResponse.StatusCode, unknownBody, wrongResponse.StatusCode, wrongBody)
	}
	unknownResponse.Body.Close()
	pendingAfterFailures, err := slice.Journal.Pending(context.Background(), 0)
	if err != nil || len(pendingAfterFailures) != len(pendingBeforeFailures) {
		t.Fatalf("unknown/cross-tenant requests wrote intent/outbox: pending=%d err=%v", len(pendingAfterFailures), err)
	}
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/requests/"+requestID+"/workflow", strings.NewReader(`{"tenant_id":"`+tenantID+`","lead_target":9}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-direct")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("direct v2 durable admission=%d: %s", response.StatusCode, readBody(t, response))
	}
	accepted := decodeView(t, response)
	if accepted["request_id"] != requestID || accepted["workflow_id"] != "fulfillment-"+requestID || accepted["attempt_id"] == "" || accepted["state"] != "queued" {
		t.Fatalf("v2 accepted response doesn't identify queued attempt: %v", accepted)
	}
	if _, ok := accepted["run_id"]; ok {
		t.Fatalf("admission invented Temporal RunID: %v", accepted)
	}
	response.Body.Close()
	resp = do(t, server, http.MethodGet, "/requests/"+requestID+"/workflow?tenant_id="+tenantID, nil)
	queuedProjection := decodeView(t, resp)
	if queuedProjection["state"] != "queued" || queuedProjection["command_id"] != "command-direct" {
		t.Fatalf("durable queued projection = %v", queuedProjection)
	}
	if starts != 0 {
		t.Fatalf("HTTP v2 direct endpoint вызвал Temporal starter %d раз", starts)
	}
	if _, err := slice.Repository.FindByID(context.Background(), "req-1"); err != nil {
		t.Fatal(err)
	}
}

func intPointer(value int) *int { return &value }
