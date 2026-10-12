package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpapi "lead-gen/backend/contexts/agent-orchestration/internal/adapters/http"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/agent-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/query"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/supervisor"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
	agentrunstatehttpport "lead-gen/contracts/agent-orchestration/agentrunstate/httpport"
)

// allowAccess — разрешающая проверка доступа тестов.
type allowAccess struct{}

func (allowAccess) CanAct(context.Context, string, string) (bool, error) { return true, nil }

// denyAccess — запрещающая проверка доступа тестов.
type denyAccess struct{}

func (denyAccess) CanAct(context.Context, string, string) (bool, error) { return false, nil }

// fakeWorkflow — тестовый двойник рантайма долгих процессов.
type fakeWorkflow struct{}

func (fakeWorkflow) StartRun(_ context.Context, in ports.StartRunInput) (ports.RunHandle, error) {
	return ports.RunHandle{WorkflowID: "wf-" + in.RunID}, nil
}
func (fakeWorkflow) RunState(context.Context, string) (ports.RunState, error) {
	return ports.RunState{}, ports.ErrWorkflowNotFound
}
func (fakeWorkflow) SignalApproval(context.Context, ports.ApprovalDecisionInput) error { return nil }
func (fakeWorkflow) StopRun(context.Context, ports.StopInput) error                    { return nil }

// fixedClock — детерминированные часы тестов.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// newServer собирает HTTP-сервер с in-memory состоянием и опубликованным пайплайном.
func newServer(t *testing.T, access ports.AccessChecker) *httpapi.Server {
	t.Helper()
	server, _ := newServerWithRepos(t, access)
	return server
}

// newServerWithRepos собирает HTTP-сервер и возвращает его хранилище: тесты,
// которым нужно перевести запуск в иное состояние (например, ожидание решения),
// правят агрегат через репозиторий напрямую.
func newServerWithRepos(t *testing.T, access ports.AccessChecker) (*httpapi.Server, inmemory.Repositories) {
	t.Helper()
	ctx := context.Background()
	repos := inmemory.New()
	uow := outboxinmemory.NewUnitOfWork(repos.Store(), platformoutbox.NewMemoryJournal())
	clock := fixedClock{at: time.Date(2026, time.June, 2, 9, 0, 0, 0, time.UTC)}
	ids := idgen.NewRandom()

	// Публикуем пайплайн в хранилище напрямую.
	block, err := domain.NewBlock("search", "search", "lead-search", false, true)
	if err != nil {
		t.Fatalf("NewBlock: %v", err)
	}
	blocks, err := domain.NewBlockRegistry(block)
	if err != nil {
		t.Fatalf("NewBlockRegistry: %v", err)
	}
	publish := command.NewPublishPipelineHandler(uow, blocks, repos.Pipelines, clock, ids)
	if _, err := publish.Handle(ctx, command.PublishPipeline{
		PipelineID: "pipe-1", TenantID: "tenant-1",
		Steps: []command.PipelineStepInput{{ID: "search", BlockRef: "search"}},
	}); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}

	sup := supervisor.New(uow, repos.Runs, fakeWorkflow{}, clock, ids, 4)
	start := command.NewStartRunHandler(uow, repos.Pipelines, repos.Runs, clock, ids, sup)
	stop := command.NewStopRunHandler(sup)
	approval := command.NewRecordApprovalHandler(uow, fakeWorkflow{}, clock, ids)
	runState := query.NewRunStateHandler(repos.Runs)
	listRuns := query.NewListRunsHandler(repos.Runs)

	return httpapi.NewServer(httpapi.Dependencies{
		StartRun:       start,
		StopRun:        stop,
		RetryStep:      sup,
		RecordApproval: approval,
		RunState:       runState,
		ListRuns:       listRuns,
		Access:         access,
	}), repos
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("маршалинг тела: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestReadyzIsBoundedStatusAndDoesNotLeakProbeErrors(t *testing.T) {
	secret := "temporal://user:private@workflow.internal?token=credential"
	server := httpapi.NewServer(httpapi.Dependencies{
		Readiness: readiness.Profile{Capability: "agent-orchestration-api", Checks: []readiness.CheckSpec{{
			ID: "temporal", Key: "agent-orchestration/temporal", Required: true,
			Probe: func(context.Context) (readiness.ProbeResult, error) {
				return readiness.ProbeResult{}, fmt.Errorf("dial %s", secret)
			},
		}}},
		ReadinessEvaluator: readiness.New(readiness.Config{Timeout: time.Second, TTL: time.Second}),
	})
	ready := doJSON(t, server, http.MethodGet, "/readyz", nil)
	if ready.Code != http.StatusServiceUnavailable || strings.Contains(ready.Body.String(), secret) || strings.Contains(ready.Body.String(), "private") || !strings.Contains(ready.Body.String(), "probe_failed") {
		t.Fatalf("/readyz leaked probe error or returned wrong status: %d %s", ready.Code, ready.Body.String())
	}
	health := doJSON(t, server, http.MethodGet, "/healthz", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("/healthz должен оставаться независимым от readiness: %d", health.Code)
	}
}

func TestAgentCommandStartAndRead(t *testing.T) {
	server := newServer(t, allowAccess{})
	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "start", "tenant_id": "tenant-1", "mission_id": "mission-1",
		"company_id": "company-1", "pipeline_id": "pipe-1", "actor": "operator-1",
		"correlation_id": "corr-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("start: код %d, тело %s", rec.Code, rec.Body.String())
	}
	var startResp struct {
		RunID    string `json:"run_id"`
		Status   string `json:"status"`
		Accepted bool   `json:"accepted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if !startResp.Accepted || startResp.RunID == "" {
		t.Fatalf("ответ на запуск = %+v", startResp)
	}

	rec = doJSON(t, server, http.MethodGet, "/agent-runs/"+startResp.RunID+"?tenant_id=tenant-1&actor=operator-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("run-state: код %d, тело %s", rec.Code, rec.Body.String())
	}
	var state struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("разбор состояния: %v", err)
	}
	if state.RunID != startResp.RunID {
		t.Fatalf("состояние не для запуска: %q", state.RunID)
	}
}

func TestAgentCommandRejectedWithoutAccess(t *testing.T) {
	server := newServer(t, denyAccess{})
	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "stop", "run_id": "run-1", "tenant_id": "tenant-1", "reason": "стоп", "actor": "operator-1",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("отказ доступа: код %d, тело %s", rec.Code, rec.Body.String())
	}
}

func TestAgentCommandHonestUnavailableWithoutAccessChecker(t *testing.T) {
	server := newServer(t, nil)
	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "stop", "run_id": "run-1", "tenant_id": "tenant-1", "reason": "стоп",
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("без проверки доступа ожидался 503, получен %d", rec.Code)
	}
}

func TestAgentCommandValidation(t *testing.T) {
	server := newServer(t, allowAccess{})
	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{"action": "start"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("некорректная команда: код %d, тело %s", rec.Code, rec.Body.String())
	}
}

// startRun поднимает агента тестового тенанта и возвращает его идентификатор.
func startRun(t *testing.T, server *httpapi.Server) string {
	t.Helper()
	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "start", "tenant_id": "tenant-1", "mission_id": "mission-1",
		"company_id": "company-1", "pipeline_id": "pipe-1", "actor": "operator-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("start: код %d, тело %s", rec.Code, rec.Body.String())
	}
	var response struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	return response.RunID
}

func TestAgentRunListReturnsTeamRuns(t *testing.T) {
	server := newServer(t, allowAccess{})
	runID := startRun(t, server)

	rec := doJSON(t, server, http.MethodGet, "/agent-runs?tenant_id=tenant-1&actor=operator-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("лента: код %d, тело %s", rec.Code, rec.Body.String())
	}
	var result agentrunstate.ListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("разбор ленты: %v", err)
	}
	if result.Count != 1 || len(result.Runs) != 1 || result.Runs[0].RunID != runID {
		t.Fatalf("лента = %+v", result)
	}
	if result.Runs[0].CompanyID != "company-1" || result.Runs[0].Status == "" {
		t.Fatalf("сводка запуска = %+v", result.Runs[0])
	}

	// Чужой тенант ленту этого тенанта не видит.
	empty := doJSON(t, server, http.MethodGet, "/agent-runs?tenant_id=tenant-2&actor=operator-1", nil)
	var foreign agentrunstate.ListResult
	if err := json.Unmarshal(empty.Body.Bytes(), &foreign); err != nil {
		t.Fatalf("разбор чужой ленты: %v", err)
	}
	if foreign.Count != 0 || len(foreign.Runs) != 0 {
		t.Fatalf("чужая лента = %+v", foreign)
	}

	// Фильтр по статусу сужает ленту.
	running := doJSON(t, server, http.MethodGet, "/agent-runs?tenant_id=tenant-1&status=running&limit=10&actor=operator-1", nil)
	if running.Code != http.StatusOK {
		t.Fatalf("лента с фильтром: код %d, тело %s", running.Code, running.Body.String())
	}
}

func TestAgentRunListValidation(t *testing.T) {
	server := newServer(t, allowAccess{})
	if rec := doJSON(t, server, http.MethodGet, "/agent-runs", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("без tenant_id: код %d", rec.Code)
	}
	if rec := doJSON(t, server, http.MethodGet, "/agent-runs?tenant_id=tenant-1&limit=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("некорректный limit: код %d", rec.Code)
	}
}

func TestAgentRunListHonestUnavailable(t *testing.T) {
	// Без проверки доступа сервер честно отвечает 503, а не выдаёт пустую ленту.
	server := newServer(t, nil)
	if rec := doJSON(t, server, http.MethodGet, "/agent-runs?tenant_id=tenant-1", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("без проверки доступа ожидался 503, получен %d", rec.Code)
	}
	// Запрещающая проверка — 403, а не пустая лента.
	server = newServer(t, denyAccess{})
	if rec := doJSON(t, server, http.MethodGet, "/agent-runs?tenant_id=tenant-1&actor=operator-1", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("без доступа ожидался 403, получен %d", rec.Code)
	}
}

// seedAwaitingApproval переводит запуск в ожидание решения по точной версии
// черновика через агрегат и сохраняет его. Так состояние awaiting-approval
// воспроизводится без прогона блока await-approval по сети.
func seedAwaitingApproval(t *testing.T, repos inmemory.Repositories, runID, draftRef string, version int) {
	t.Helper()
	ctx := context.Background()
	run, err := repos.Runs.FindByID(ctx, domain.RunID(runID), "tenant-1")
	if err != nil {
		t.Fatalf("загрузка запуска: %v", err)
	}
	revision := run.Revision()
	at := time.Date(2026, time.June, 2, 10, 0, 0, 0, time.UTC)
	if err := run.RequestApproval(domain.DraftRef(draftRef), version, domain.ActorID("operator-1"), at, domain.EventHeader{}); err != nil {
		t.Fatalf("перевод в ожидание утверждения: %v", err)
	}
	if err := repos.Runs.Save(ctx, run, revision); err != nil {
		t.Fatalf("сохранение ожидающего запуска: %v", err)
	}
}

// TestAgentCommandApprovalResumesRun проверяет успешное решение: утверждение
// возобновляет запуск, стоявший в ожидании точной версии черновика.
func TestAgentCommandApprovalResumesRun(t *testing.T) {
	server, repos := newServerWithRepos(t, allowAccess{})
	runID := startRun(t, server)
	seedAwaitingApproval(t, repos, runID, "company-1@v2", 2)

	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "approve", "run_id": runID, "tenant_id": "tenant-1",
		"draft_ref": "company-1@v2", "draft_version": 2, "actor": "operator-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: код %d, тело %s", rec.Code, rec.Body.String())
	}
	var resp commandResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if !resp.Accepted || resp.Status != string(domain.StatusRunning) {
		t.Fatalf("ответ на утверждение = %+v", resp)
	}

	state := doJSON(t, server, http.MethodGet, "/agent-runs/"+runID+"?tenant_id=tenant-1&actor=operator-1", nil)
	if state.Code != http.StatusOK {
		t.Fatalf("состояние: код %d, тело %s", state.Code, state.Body.String())
	}
}

// TestAgentCommandRejectionNeedsAttention проверяет отказ: отправка не
// выполняется, запуск переходит в требующее вмешательства состояние.
func TestAgentCommandRejectionNeedsAttention(t *testing.T) {
	server, repos := newServerWithRepos(t, allowAccess{})
	runID := startRun(t, server)
	seedAwaitingApproval(t, repos, runID, "company-1@v2", 2)

	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "reject", "run_id": runID, "tenant_id": "tenant-1",
		"draft_ref": "company-1@v2", "draft_version": 2, "reason": "не тот тон письма", "actor": "operator-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("reject: код %d, тело %s", rec.Code, rec.Body.String())
	}
	var resp commandResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if resp.Status != string(domain.StatusNeedsAttention) {
		t.Fatalf("ответ на отказ = %+v", resp)
	}
}

// TestAgentCommandApprovalRejectsChangedDraft проверяет, что решение по чужой
// версии черновика отвергается и состояние запуска не меняется.
func TestAgentCommandApprovalRejectsChangedDraft(t *testing.T) {
	server, repos := newServerWithRepos(t, allowAccess{})
	runID := startRun(t, server)
	seedAwaitingApproval(t, repos, runID, "company-1@v2", 2)

	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "approve", "run_id": runID, "tenant_id": "tenant-1",
		"draft_ref": "company-1@v3", "draft_version": 3, "actor": "operator-1",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("решение по чужой версии: код %d, тело %s", rec.Code, rec.Body.String())
	}
}

// TestAgentCommandApprovalRejectsWithoutPending проверяет, что решение по
// запуску вне ожидания отвергается.
func TestAgentCommandApprovalRejectsWithoutPending(t *testing.T) {
	server, _ := newServerWithRepos(t, allowAccess{})
	runID := startRun(t, server)

	rec := doJSON(t, server, http.MethodPost, "/agent-commands", map[string]any{
		"action": "approve", "run_id": runID, "tenant_id": "tenant-1",
		"draft_ref": "company-1@v2", "draft_version": 2, "actor": "operator-1",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("решение без ожидания: код %d, тело %s", rec.Code, rec.Body.String())
	}
}

// TestAgentCommandApprovalValidation проверяет форму команды решения: без
// черновика/версии и отказ без причины отвергаются как некорректные.
func TestAgentCommandApprovalValidation(t *testing.T) {
	server, _ := newServerWithRepos(t, allowAccess{})
	cases := map[string]map[string]any{
		"approve без черновика": {"action": "approve", "run_id": "r", "tenant_id": "tenant-1", "draft_version": 2},
		"approve без версии":    {"action": "approve", "run_id": "r", "tenant_id": "tenant-1", "draft_ref": "d@v2"},
		"reject без причины":    {"action": "reject", "run_id": "r", "tenant_id": "tenant-1", "draft_ref": "d@v2", "draft_version": 2},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, server, http.MethodPost, "/agent-commands", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("код %d, тело %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// commandResponseBody — форма ответа владельца на команду (тестовый разбор).
type commandResponseBody struct {
	RunID    string `json:"run_id"`
	Status   string `json:"status"`
	Accepted bool   `json:"accepted"`
}

func TestRunStateHTTPContractMatchesClient(t *testing.T) {
	server := newServer(t, allowAccess{})
	runID := startRun(t, server)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client, err := agentrunstatehttpport.New(httpServer.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}

	state, err := client.RunState(context.Background(), agentrunstate.Request{RunID: runID, TenantID: "tenant-1", Actor: "operator-1"})
	if err != nil {
		t.Fatalf("клиентский RunState: %v", err)
	}
	if state.RunID != runID || len(state.Steps) != 1 {
		t.Fatalf("состояние от клиента = %+v", state)
	}

	summaries, err := client.List(context.Background(), agentrunstate.ListRequest{TenantID: "tenant-1", Actor: "operator-1"})
	if err != nil {
		t.Fatalf("клиентский List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].RunID != runID {
		t.Fatalf("лента от клиента = %+v", summaries)
	}
}
