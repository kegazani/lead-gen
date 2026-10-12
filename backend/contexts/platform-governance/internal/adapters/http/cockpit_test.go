package httpapi_test

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/panels"
	"lead-gen/backend/platform/workflow"
)

// fakeOperations — управляемый двойник владельца данных экрана операций (только
// в тестах: в поставке nil означает честное «недоступно»).
type fakeOperations struct {
	numbers httpapi.ClientNumbersResult
	agents  []httpapi.AgentRow
	detail  httpapi.AgentDetail
	clients []httpapi.ClientAggregate
	err     error
}

func (f fakeOperations) Numbers(context.Context, time.Duration) (httpapi.ClientNumbersResult, error) {
	return f.numbers, f.err
}

func (f fakeOperations) Agents(context.Context, httpapi.AgentFilter) ([]httpapi.AgentRow, error) {
	return f.agents, f.err
}

func (f fakeOperations) AgentDetail(context.Context, string, string) (httpapi.AgentDetail, error) {
	return f.detail, f.err
}

func (f fakeOperations) Clients(context.Context, time.Duration) ([]httpapi.ClientAggregate, error) {
	return f.clients, f.err
}

// fakeWorkflow — управляемый двойник чтения процессов Temporal.
type fakeWorkflow struct {
	executions []workflow.ProcessExecution
	process    workflow.Process
	err        error
}

type fakeRequestExecutions struct {
	requests       []httpapi.RequestSummary
	executions     map[string]platformgovernance.RequestExecution
	listErr        error
	workflowErr    error
	listedTenant   string
	workflowTenant string
	workflowID     string
}

func (f *fakeRequestExecutions) ListRequests(_ context.Context, tenantID string) ([]httpapi.RequestSummary, error) {
	f.listedTenant = tenantID
	return f.requests, f.listErr
}

func (f *fakeRequestExecutions) Workflow(_ context.Context, tenantID, requestID string) (platformgovernance.RequestExecution, error) {
	f.workflowTenant = tenantID
	f.workflowID = requestID
	if f.workflowErr != nil {
		return platformgovernance.RequestExecution{}, f.workflowErr
	}
	return f.executions[requestID], nil
}

func (f fakeWorkflow) List(context.Context, string, int) ([]workflow.ProcessExecution, error) {
	return f.executions, f.err
}

func (f fakeWorkflow) Timeline(context.Context, string, string) (workflow.Process, error) {
	return f.process, f.err
}

// csrfFieldRe вырезает токен CSRF из скрытого поля формы экрана.
var csrfFieldRe = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

// TestCockpitNumbersClickThrough (6.1) проверяет цифры процесса с разбивкой по
// клиенту и кликабельный переход в срез.
func TestCockpitNumbersClickThrough(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: fakeOperations{
		numbers: httpapi.ClientNumbersResult{
			Clients: []httpapi.ClientNumbers{
				{ClientID: "tenant-1", Found: 12, Processed: 8, Rejected: 3, InProgress: 1, Result: 2},
			},
			Periods: []httpapi.NumberPeriod{
				{Date: "2026-10-06", Found: 12, Processed: 8, Rejected: 3},
			},
		},
	}})
	page := body(t, get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{"tenant-1", "12", "category=rejected", "client_id=tenant-1", "Разбивка по времени", "2026-10-06"} {
		if !strings.Contains(page, want) {
			t.Fatalf("экран цифр не содержит %q:\n%s", want, page)
		}
	}
}

// TestCockpitNumbersUnavailable (6.1) проверяет честное «недоступно» без нулей,
// когда владелец данных не подключён.
func TestCockpitNumbersUnavailable(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{})
	page := body(t, get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, "Данные процесса недоступны") {
		t.Fatalf("нет честного «недоступно»:\n%s", page)
	}
	if strings.Contains(page, ">0<") {
		t.Fatalf("недоступность подменена нулями:\n%s", page)
	}
	// Панели без подключённых источников (метрики/процессы) тоже честно недоступны.
	if !strings.Contains(page, "data-panel-id=\"operations_workflow_started\"") || !strings.Contains(page, "data-panel-id=\"process_overview\"") {
		t.Fatalf("встроенные панели не объявлены:\n%s", page)
	}
	if !strings.Contains(page, "недоступно") {
		t.Fatalf("панель без источника не показала недоступность:\n%s", page)
	}
}

func TestCockpitRequestsExecutionReadIsTenantScopedAndKeepsDomainStatus(t *testing.T) {
	source := &fakeRequestExecutions{
		requests: []httpapi.RequestSummary{{RequestID: "req-42", DomainStatus: "fulfilled"}},
		executions: map[string]platformgovernance.RequestExecution{
			"req-42": {
				RequestID: "req-42", WorkflowID: "wf-42", RunID: "run-9", Phase: "Completed",
				ExecutionFound: true, Found: 1, Target: 4,
			},
		},
	}
	server := newTopologyServer(t, platformgovernance.Config{RequestExecutions: source})
	page := body(t, get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{"Заявки и исполнение", "req-42", "Выполнена", "Completed", "wf-42 / run-9", "1 / 4"} {
		if !strings.Contains(page, want) {
			t.Fatalf("блок заявки/исполнения не содержит %q:\n%s", want, page)
		}
	}
	if source.listedTenant != "tenant-1" || source.workflowTenant != "tenant-1" || source.workflowID != "req-42" {
		t.Fatalf("read source вызван без точного scope: list tenant=%q workflow tenant=%q request=%q", source.listedTenant, source.workflowTenant, source.workflowID)
	}
	if strings.Contains(page, "Completed</td>\n        <td>Выполнена") {
		t.Fatal("статус workflow Completed ошибочно выведен как доменное fulfilled")
	}
}

func TestCockpitRequestDomainStatusLabels(t *testing.T) {
	statuses := []struct {
		code string
		want string
	}{
		{code: "submitted", want: "Создана"},
		{code: "validating", want: "Создана"},
		{code: "validated", want: "Создана"},
		{code: "processing", want: "В работе"},
		{code: "cancelled", want: "Отменена"},
		{code: "fulfilled", want: "Выполнена"},
		{code: "rejected", want: "Отклонена"},
		{code: "needs-revision", want: "Требует доработки"},
		{code: "failed", want: "Сбой"},
	}
	requests := make([]httpapi.RequestSummary, 0, len(statuses))
	for index, status := range statuses {
		requests = append(requests, httpapi.RequestSummary{
			RequestID: "req-status-" + strconv.Itoa(index), DomainStatus: status.code,
		})
	}
	server := newTopologyServer(t, platformgovernance.Config{
		RequestExecutions: &fakeRequestExecutions{requests: requests},
	})
	page := body(t, get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	for _, status := range statuses {
		if !strings.Contains(page, ">"+status.want+"</td>") {
			t.Errorf("статус %q показан не как %q:\n%s", status.code, status.want, page)
		}
	}
}

func TestCockpitRequestsDistinguishesEmptyAndUnavailable(t *testing.T) {
	empty := newTopologyServer(t, platformgovernance.Config{RequestExecutions: &fakeRequestExecutions{requests: []httpapi.RequestSummary{}}})
	emptyPage := body(t, get(t, empty, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(emptyPage, "Заявок пока нет") || strings.Contains(emptyPage, "Список заявок недоступен") {
		t.Fatalf("пустой результат не отличим от отказа:\n%s", emptyPage)
	}

	unavailable := newTopologyServer(t, platformgovernance.Config{})
	page := body(t, get(t, unavailable, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, "Список заявок недоступен") || !strings.Contains(page, "cockpit-panel") {
		t.Fatalf("недоступность блока не показана локально либо остальные панели пропали:\n%s", page)
	}

	broken := newTopologyServer(t, platformgovernance.Config{RequestExecutions: &fakeRequestExecutions{
		requests: []httpapi.RequestSummary{{RequestID: "req-5", DomainStatus: "processing"}}, workflowErr: context.DeadlineExceeded,
	}})
	brokenPage := body(t, get(t, broken, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(brokenPage, "req-5") || !strings.Contains(brokenPage, "Исполнение недоступно") {
		t.Fatalf("ошибка workflow должна сохранять строку заявки и локально показывать отказ:\n%s", brokenPage)
	}
	if strings.Contains(brokenPage, "0 / 0") {
		t.Fatal("отсутствующая execution data подменена нулевыми значениями")
	}
}

func TestCockpitRequestsRemainBehindAuthorization(t *testing.T) {
	source := &fakeRequestExecutions{requests: []httpapi.RequestSummary{{RequestID: "secret-request", DomainStatus: "processing"}}}
	server := newTopologyServer(t, platformgovernance.Config{RequestExecutions: source})
	unauthenticated := get(t, server, "/platform/operations?tenant_id=tenant-1")
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("без актора получен %d, ожидался 401", unauthenticated.StatusCode)
	}
	if source.listedTenant != "" {
		t.Fatalf("источник прочитан до проверки доступа: tenant=%q", source.listedTenant)
	}

	denied := get(t, server, "/platform/operations?tenant_id=tenant-2&actor=user-3")
	deniedBody := body(t, denied)
	if denied.StatusCode != http.StatusForbidden || strings.Contains(deniedBody, "secret-request") {
		t.Fatalf("чужой запрос раскрыл данные: status=%d body=%s", denied.StatusCode, deniedBody)
	}
	if source.listedTenant != "" {
		t.Fatalf("источник прочитан до запрета доступа: tenant=%q", source.listedTenant)
	}
}

// TestCockpitAgentsLiveFeed (6.2) проверяет живую ленту: авто-обновление через
// HTMX, раскрытие строки как <details> с hx-preserve, фрагмент без каркаса.
func TestCockpitAgentsLiveFeed(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: fakeOperations{
		agents: []httpapi.AgentRow{
			{RunID: "run-1", ClientID: "tenant-1", Company: "ООО Ромашка", State: "running", Step: "enrich", UpdatedAt: time.Now().UTC()},
		},
	}})
	page := body(t, get(t, server, "/platform/operations/agents?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{
		"id=\"agents-feed\"",
		"hx-get=\"/platform/operations/agents?",
		"hx-trigger=\"every 5s\"",
		"hx-preserve=\"true\"",
		"id=\"agent-row-run-1\"",
		"/static/htmx.min.js",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("лента не содержит %q:\n%s", want, page)
		}
	}

	fragment := body(t, htmxGet(t, server, "/platform/operations/agents?tenant_id=tenant-1&actor=user-1"))
	if strings.Contains(fragment, "<!DOCTYPE") {
		t.Fatalf("HTMX-фрагмент содержит каркас страницы:\n%s", fragment)
	}
	if !strings.Contains(fragment, "agent-row-run-1") {
		t.Fatalf("фрагмент не содержит строк ленты:\n%s", fragment)
	}
}

// TestCockpitAgentDetail (6.3) проверяет подробности: события, нормализованные
// причины и связанные задания; отказ показан отказом.
func TestCockpitAgentDetail(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: fakeOperations{
		detail: httpapi.AgentDetail{
			RunID:    "run-1",
			ClientID: "tenant-1",
			Company:  "ООО Ромашка",
			State:    "failed",
			Events: []httpapi.AgentEvent{
				{At: time.Now().UTC(), Step: "enrich", Kind: "activity", Outcome: "failed", Reason: "extraction_error"},
			},
			Reasons: []httpapi.AgentReason{{Code: "extraction_error", Count: 2}},
			Jobs:    []httpapi.AgentJob{{ID: "job-1", Kind: "activity", State: "failed"}},
		},
	}})
	page := body(t, get(t, server, "/platform/operations/agents/run-1?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{
		"run-1",
		"ООО Ромашка",
		"ошибка извлечения",
		"job-1",
		"отказ",
		"К ленте",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("подробности не содержат %q:\n%s", want, page)
		}
	}
}

// TestCockpitProcessGraphAndUnavailable (6.4) проверяет граф процессов Temporal
// и честное «недоступно» при отсутствии рантайма.
func TestCockpitProcessGraphAndUnavailable(t *testing.T) {
	reader := fakeWorkflow{
		executions: []workflow.ProcessExecution{
			{WorkflowID: "wf-1", RunID: "run-1", Type: "AgentRun", Status: workflow.ProcessRunning, Running: true, StartTime: time.Now().UTC()},
			{WorkflowID: "wf-2", RunID: "run-2", Type: "RequestFlow", Status: workflow.ProcessCompleted},
		},
		process: workflow.Process{
			Steps: []workflow.ProcessStep{{Name: "search", Kind: workflow.StepKindActivity, Outcome: workflow.StepCompleted, At: time.Now().UTC()}},
			Waits: []workflow.ProcessWait{{Name: "await-approval", Kind: workflow.WaitKindTimer, State: workflow.WaitStarted, At: time.Now().UTC()}},
		},
	}
	server := newTopologyServer(t, platformgovernance.Config{WorkflowReader: reader})
	page := body(t, get(t, server, "/platform/operations/process?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{
		"data-cockpit-graph",
		"data-node-id=\"wf-1\"",
		"AgentRun",
		"await-approval",
		"hx-trigger=\"every 5s\"",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("граф процессов не содержит %q:\n%s", want, page)
		}
	}

	// Недоступный рантайм — явное «недоступно», а не пустой граф.
	unavailable := newTopologyServer(t, platformgovernance.Config{})
	off := body(t, get(t, unavailable, "/platform/operations/process?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(off, "Процессы Temporal недоступны") || strings.Contains(off, "data-cockpit-graph") {
		t.Fatalf("пустой граф выдан за успех:\n%s", off)
	}

	// Ошибка чтения рантайма — тот же честный исход.
	broken := newTopologyServer(t, platformgovernance.Config{WorkflowReader: fakeWorkflow{err: context.DeadlineExceeded}})
	brokenPage := body(t, get(t, broken, "/platform/operations/process?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(brokenPage, "Процессы Temporal недоступны") {
		t.Fatalf("сбой рантайма не показан как недоступность:\n%s", brokenPage)
	}
}

// TestCockpitDashboardThreshold (6.5) проверяет полноэкранный режим и порог
// минимальной группы: срез ниже порога не показывается.
func TestCockpitDashboardThreshold(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{
		CockpitMinGroup: 5,
		Operations: fakeOperations{
			clients: []httpapi.ClientAggregate{
				{ClientID: "tenant-big", Found: 10, Processed: 5, Anomalies: []string{"stalled"}},
				{ClientID: "tenant-small", Found: 2},
			},
		},
	})
	page := body(t, get(t, server, "/platform/operations/dashboard?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, "data-fullscreen") {
		t.Fatalf("дашборд не в полноэкранном режиме:\n%s", page)
	}
	if !strings.Contains(page, "tenant-big") || !strings.Contains(page, "процесс замер") {
		t.Fatalf("дашборд не показал крупный срез:\n%s", page)
	}
	if strings.Contains(page, "tenant-small") {
		t.Fatalf("показан срез ниже порога минимальной группы:\n%s", page)
	}
}

// TestCockpitPanelsRegistrationAndIsolation (5.4) проверяет, что панель
// добавляется регистрацией, а отказ одной панели не ломает остальные.
func TestCockpitPanelsRegistrationAndIsolation(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{
		CockpitMetrics: fakeMetrics{matrix: prometheus.Matrix{
			{Metric: map[string]string{"service": "s"}, Samples: []prometheus.Sample{{Time: time.Now().UTC(), Value: 7}}},
		}},
		CockpitPanels: []httpapi.CockpitPanel{
			{
				Definition: panels.Definition{ID: "custom-ok", Title: "Своя панель", Source: "aggregate", Refresh: panels.RefreshStatic, Order: 30},
				Render: func(context.Context) (template.HTML, error) {
					return template.HTML(`<p class="custom-panel-body">значение</p>`), nil
				},
			},
			{
				Definition: panels.Definition{ID: "custom-bad", Title: "Сломанная панель", Source: "aggregate", Refresh: panels.RefreshStatic, Order: 31},
				Render: func(context.Context) (template.HTML, error) {
					return "", context.DeadlineExceeded
				},
			},
		},
	})
	page := body(t, get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, "custom-panel-body") {
		t.Fatalf("зарегистрированная панель не появилась:\n%s", page)
	}
	if !strings.Contains(page, "data-panel-id=\"custom-bad\"") || !strings.Contains(page, "недоступно") {
		t.Fatalf("отказ панели не локализован:\n%s", page)
	}
	// Встроенные панели продолжают работать: метрики отдают число.
	if !strings.Contains(page, "cockpit-panel-number") {
		t.Fatalf("отказ одной панели сломал метрики:\n%s", page)
	}
}

// TestCockpitPanelsToggleCSRF (7.2) проверяет защиту изменяющей формы токеном
// CSRF: без токена — отказ, с выданным токеном — переключение панели.
func TestCockpitPanelsToggleCSRF(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{})
	pageResp := get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1")
	page := body(t, pageResp)
	match := csrfFieldRe.FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("не найден токен CSRF на экране:\n%s", page)
	}
	token := match[1]

	// Без токена — отказ.
	denied := postForm(t, server, "/platform/operations/panels/toggle?tenant_id=tenant-1&actor=user-1",
		url.Values{"panel": {"process_overview"}, "hidden": {"true"}})
	denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("POST без токена = %d, ожидался 403", denied.StatusCode)
	}

	// С выданным токеном — переключение.
	form := url.Values{"csrf": {token}, "panel": {"process_overview"}, "hidden": {"true"}}
	request, err := http.NewRequest(http.MethodPost,
		server.URL+"/platform/operations/panels/toggle?tenant_id=tenant-1&actor=user-1",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "csrf", Value: token})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST с токеном: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST с токеном = %d, ожидался 303", resp.StatusCode)
	}
}

// TestCockpitNavigationLink (6.7) проверяет, что общая навигация ведёт на экран
// операций по образцу консоли топологии и что ссылка доступна администратору.
func TestCockpitNavigationLink(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{})
	page := body(t, get(t, server, "/platform/topology?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, "Операции") {
		t.Fatalf("в навигации нет ссылки на экран операций:\n%s", page)
	}
	if !strings.Contains(page, "/platform/operations?") {
		t.Fatalf("ссылка на экран операций не ведёт на маршрут:\n%s", page)
	}
}

// TestCockpitDeniedForNonAdmin (7.1) проверяет, что отказ не зависит от
// навигации: не-администратор получает отказ на каждом эндпоинте.
func TestCockpitDeniedForNonAdmin(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: fakeOperations{
		numbers: httpapi.ClientNumbersResult{Clients: []httpapi.ClientNumbers{{ClientID: "tenant-2", Found: 1}}},
	}})
	paths := []string{
		"/platform/operations?tenant_id=tenant-2&actor=user-3",
		"/platform/operations/agents?tenant_id=tenant-2&actor=user-3",
		"/platform/operations/agents/run-1?tenant_id=tenant-2&actor=user-3",
		"/platform/operations/process?tenant_id=tenant-2&actor=user-3",
		"/platform/operations/dashboard?tenant_id=tenant-2&actor=user-3",
	}
	for _, path := range paths {
		resp := get(t, server, path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, ожидался 403", path, resp.StatusCode)
		}
	}

	admin := get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1")
	admin.Body.Close()
	if admin.StatusCode != http.StatusOK {
		t.Fatalf("администратор = %d, ожидался 200", admin.StatusCode)
	}
}
