// Package httpapi реализует HTTP-адаптер роли api контекста agent-orchestration:
// контракты agent-command (управляющие команды агенту) и agent-run-state (чтение
// состояния запуска).
//
// Адаптер только разбирает запрос, проверяет доступ, вызывает прикладной сценарий и
// переводит доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивает aggregate root agent-run, а транзакцию — прикладной слой.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/query"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/supervisor"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/agent-orchestration/agentcommand"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

const (
	// maxBodyBytes ограничивает размер тела запроса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
)

// RuntimeProbe — признак доступности рантайма долгих процессов для пробы
// готовности (/readyz). Реализуется platform/workflow.Availability.
type RuntimeProbe interface {
	Ready() bool
}

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии контекста и
// проверка доступа.
type Dependencies struct {
	// StartRun — запуск агента (команда start).
	StartRun *command.StartRunHandler
	// StopRun — остановка агента (команда stop).
	StopRun *command.StopRunHandler
	// RetryStep — повтор шага (команда retry).
	RetryStep *supervisor.Supervisor
	// RunStep — прогон текущего шага (исполнение повтора и воркер).
	RunStep *command.RunStepHandler
	// RecordApproval — решение оператора по черновику.
	RecordApproval *command.RecordApprovalHandler
	// RunState — чтение состояния запуска (agent-run-state).
	RunState *query.RunStateHandler
	// ListRuns — чтение списка (ленты) запусков (agent-run-state).
	ListRuns *query.ListRunsHandler

	// Access — проверка права актора действовать в тенанте (access-decision). nil
	// означает, что владелец решения не подключён: сервер честно отвечает 503, а не
	// пропускает команду без проверки.
	Access ports.AccessChecker

	// Runtime — monitor-backed факт доступности Temporal. nil считается unknown.
	Runtime RuntimeProbe
	// Readiness — явный профиль readiness владельца контекста.
	Readiness readiness.Profile
	// ReadinessEvaluator ограничивает и кэширует проверки профиля.
	ReadinessEvaluator *readiness.Evaluator
}

// Server — HTTP-сервер роли api. Нулевое значение непригодно: используйте NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
}

// NewServer собирает маршруты контрактов из переданных сценариев.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps}
	server.handler = server.routes()
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты контрактов контекста.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent-commands", s.handleCommand)
	mux.HandleFunc("GET /agent-runs", s.handleRunList)
	mux.HandleFunc("GET /agent-runs/{runID}", s.handleRunState)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	return mux
}

// commandBody — тело команды agent-command.
type commandBody struct {
	Action          string `json:"action"`
	RunID           string `json:"run_id"`
	TenantID        string `json:"tenant_id"`
	MissionID       string `json:"mission_id"`
	CompanyID       string `json:"company_id"`
	PipelineID      string `json:"pipeline_id"`
	PipelineVersion int    `json:"pipeline_version"`
	Criteria        string `json:"criteria"`
	DraftRef        string `json:"draft_ref"`
	DraftVersion    int    `json:"draft_version"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
	CorrelationID   string `json:"correlation_id"`
	CausationID     string `json:"causation_id"`
}

// commandResponse — ответ на команду agent-command.
type commandResponse struct {
	RunID    string `json:"run_id"`
	Status   string `json:"status"`
	Accepted bool   `json:"accepted"`
	Repeated bool   `json:"repeated"`
}

// handleCommand обслуживает POST /agent-commands (контракт agent-command).
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	var body commandBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cmd := agentcommand.Command{
		Action:          agentcommand.Action(body.Action),
		RunID:           body.RunID,
		TenantID:        body.TenantID,
		MissionID:       body.MissionID,
		CompanyID:       body.CompanyID,
		PipelineID:      body.PipelineID,
		PipelineVersion: body.PipelineVersion,
		Criteria:        body.Criteria,
		DraftRef:        body.DraftRef,
		DraftVersion:    body.DraftVersion,
		Actor:           body.Actor,
		Reason:          body.Reason,
		CorrelationID:   body.CorrelationID,
		CausationID:     body.CausationID,
	}
	if err := cmd.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.authorize(w, r, cmd.TenantID, cmd.Actor) {
		return
	}

	switch cmd.Action {
	case agentcommand.ActionStart:
		s.runStart(w, r.Context(), cmd)
	case agentcommand.ActionStop:
		s.runStop(w, r.Context(), cmd)
	case agentcommand.ActionRetry:
		s.runRetry(w, r.Context(), cmd)
	case agentcommand.ActionApprove:
		s.runApproval(w, r.Context(), cmd, true)
	case agentcommand.ActionReject:
		s.runApproval(w, r.Context(), cmd, false)
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("неизвестное действие %q", cmd.Action))
	}
}

// runStart выполняет команду запуска агента.
func (s *Server) runStart(w http.ResponseWriter, ctx context.Context, cmd agentcommand.Command) {
	if s.deps.StartRun == nil {
		writeError(w, http.StatusServiceUnavailable, "сценарий запуска не собран")
		return
	}
	result, err := s.deps.StartRun.Handle(ctx, command.StartRun{
		RunID:           cmd.RunID,
		TenantID:        cmd.TenantID,
		MissionID:       cmd.MissionID,
		CompanyID:       cmd.CompanyID,
		PipelineID:      cmd.PipelineID,
		PipelineVersion: cmd.PipelineVersion,
		Criteria:        cmd.Criteria,
		CorrelationID:   cmd.CorrelationID,
		CausationID:     cmd.CausationID,
	})
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commandResponse{RunID: result.RunID, Status: string(result.Status), Accepted: true})
}

// runStop выполняет команду остановки агента.
func (s *Server) runStop(w http.ResponseWriter, ctx context.Context, cmd agentcommand.Command) {
	if s.deps.StopRun == nil {
		writeError(w, http.StatusServiceUnavailable, "сценарий остановки не собран")
		return
	}
	if err := s.deps.StopRun.Handle(ctx, command.StopRun{
		RunID:         cmd.RunID,
		TenantID:      cmd.TenantID,
		Actor:         cmd.Actor,
		Reason:        cmd.Reason,
		CorrelationID: cmd.CorrelationID,
		CausationID:   cmd.CausationID,
	}); err != nil {
		s.writeCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commandResponse{RunID: cmd.RunID, Status: string(domain.StatusStopped), Accepted: true})
}

// runRetry выполняет команду повтора текущего неуспешного шага.
func (s *Server) runRetry(w http.ResponseWriter, ctx context.Context, cmd agentcommand.Command) {
	if s.deps.RetryStep == nil || s.deps.RunStep == nil {
		writeError(w, http.StatusServiceUnavailable, "сценарий повтора не собран")
		return
	}
	retryable, err := s.deps.RetryStep.RetryStep(ctx, domain.RunID(cmd.RunID), domain.TenantID(cmd.TenantID))
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	if !retryable {
		writeError(w, http.StatusConflict, "повтор шага невозможен: попытки исчерпаны или шаг не в отказе")
		return
	}
	result, err := s.deps.RunStep.Handle(ctx, command.RunStep{
		RunID: cmd.RunID, TenantID: cmd.TenantID, CorrelationID: cmd.CorrelationID, CausationID: cmd.CausationID,
	})
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commandResponse{RunID: cmd.RunID, Status: string(result.RunStatus), Accepted: true})
}

// runApproval выполняет команду решения оператора по запуску в ожидании
// утверждения. Решение адресует точную версию ожидающего черновика и передаётся
// сценарию RecordApproval, который фиксирует его и подаёт сигнал бегущему
// исполнению. Поверхность не санкционирует отправку сама: шаг send перечитывает
// решение владельца очереди утверждения.
func (s *Server) runApproval(w http.ResponseWriter, ctx context.Context, cmd agentcommand.Command, approved bool) {
	if s.deps.RecordApproval == nil {
		writeError(w, http.StatusServiceUnavailable, "сценарий решения не собран")
		return
	}
	if err := s.deps.RecordApproval.Handle(ctx, command.RecordApproval{
		RunID:         cmd.RunID,
		TenantID:      cmd.TenantID,
		DraftRef:      cmd.DraftRef,
		DraftVersion:  cmd.DraftVersion,
		Approved:      approved,
		Actor:         cmd.Actor,
		Reason:        cmd.Reason,
		CorrelationID: cmd.CorrelationID,
		CausationID:   cmd.CausationID,
	}); err != nil {
		s.writeCommandError(w, err)
		return
	}
	status := string(domain.StatusRunning)
	if !approved {
		status = string(domain.StatusNeedsAttention)
	}
	writeJSON(w, http.StatusOK, commandResponse{RunID: cmd.RunID, Status: status, Accepted: true})
}

// handleRunState обслуживает GET /agent-runs/{runID} (контракт agent-run-state).
func (s *Server) handleRunState(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	tenant := r.URL.Query().Get("tenant_id")
	actor := r.URL.Query().Get("actor")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "не задан tenant_id")
		return
	}
	if !s.authorize(w, r, tenant, actor) {
		return
	}
	if s.deps.RunState == nil {
		writeError(w, http.StatusServiceUnavailable, "чтение состояния не собрано")
		return
	}
	state, err := s.deps.RunState.Handle(r.Context(), query.RunStateQuery{RunID: runID, TenantID: tenant})
	if err != nil {
		if errors.Is(err, agentrunstate.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.writeCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// handleRunList обслуживает GET /agent-runs (контракт agent-run-state, список
// запусков). Форма ответа — agentrunstate.ListResult; недоступность владельца
// отдаётся кодом, а не пустым списком (пустой список — корректный результат).
func (s *Server) handleRunList(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	actor := r.URL.Query().Get("actor")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "не задан tenant_id")
		return
	}
	if !s.authorize(w, r, tenant, actor) {
		return
	}
	if s.deps.ListRuns == nil {
		writeError(w, http.StatusServiceUnavailable, "чтение ленты запусков не собрано")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "некорректный limit")
			return
		}
		limit = parsed
	}
	summaries, err := s.deps.ListRuns.Handle(r.Context(), query.ListRunsQuery{
		TenantID: tenant,
		Status:   r.URL.Query().Get("status"),
		Limit:    limit,
	})
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentrunstate.ListResult{Runs: summaries, Count: len(summaries)})
}

// authorize проверяет право актора действовать в тенанте. Без подключённого
// владельца решения доступ не выдаётся: сервер честно отвечает 503, а не пропускает
// запрос без проверки.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, tenant, actor string) bool {
	if s.deps.Access == nil {
		writeError(w, http.StatusServiceUnavailable, "проверка доступа недоступна")
		return false
	}
	allowed, err := s.deps.Access.CanAct(r.Context(), actor, tenant)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "проверка доступа недоступна")
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "доступ запрещён")
		return false
	}
	return true
}

// writeCommandError переводит доменную ошибку и ошибку порта в код ответа.
func (s *Server) writeCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrRunNotFound), errors.Is(err, ports.ErrPipelineNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ports.ErrVersionConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ports.ErrWorkflowUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, domain.ErrTerminalRun),
		errors.Is(err, domain.ErrInvalidRunTransition),
		errors.Is(err, domain.ErrStepNotStarted),
		errors.Is(err, domain.ErrNoCurrentStep),
		errors.Is(err, domain.ErrApprovalRequired),
		errors.Is(err, domain.ErrDraftChanged),
		errors.Is(err, domain.ErrNoApprovalPending),
		errors.Is(err, domain.ErrUnregisteredBlock),
		errors.Is(err, domain.ErrPipelineAlreadyPublished):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleHealth — проба живости.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz отражает готовность с учётом доступности рантайма долгих процессов.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	evaluator := s.deps.ReadinessEvaluator
	if evaluator == nil {
		evaluator = readiness.New(readiness.Config{Timeout: 750 * time.Millisecond, TTL: 2 * time.Second})
	}
	snapshot := evaluator.Evaluate(r.Context(), s.deps.Readiness)
	status := http.StatusOK
	if !snapshot.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, snapshot)
}

// decodeJSON читает тело запроса в dst, отвергая неизвестные поля.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("тело запроса отсутствует")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

// writeJSON отправляет полезную нагрузку с заданным статусом.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError отправляет сообщение об ошибке.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
