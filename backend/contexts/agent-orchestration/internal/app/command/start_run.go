package command

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/supervisor"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// StartRun — вход запуска агента для компании в контексте задания.
type StartRun struct {
	// RunID — идентификатор запуска. Пусто — владелец порождает идентификатор.
	RunID      string
	TenantID   string
	MissionID  string
	CompanyID  string
	PipelineID string
	// PipelineVersion — версия пайплайна. 0 — последняя опубликованная.
	PipelineVersion int
	Criteria        string
	CorrelationID   string
	CausationID     string
}

// StartRunResult — наблюдаемый исход запуска агента.
type StartRunResult struct {
	RunID           string
	PipelineVersion int
	Status          domain.RunStatus
}

// StartRunHandler порождает запуск агента и передаёт его супервизору: привязка к
// опубликованной версии пайплайна, тенант-скоуп и постановка в очередь.
type StartRunHandler struct {
	uow        ports.UnitOfWork
	pipelines  ports.PipelineRepository
	runs       ports.RunRepository
	clock      ports.Clock
	ids        ports.IDGenerator
	supervisor *supervisor.Supervisor
}

// NewStartRunHandler собирает сценарий запуска агента.
func NewStartRunHandler(uow ports.UnitOfWork, pipelines ports.PipelineRepository, runs ports.RunRepository, clock ports.Clock, ids ports.IDGenerator, sup *supervisor.Supervisor) *StartRunHandler {
	return &StartRunHandler{uow: uow, pipelines: pipelines, runs: runs, clock: clock, ids: ids, supervisor: sup}
}

// Handle создаёт запуск по опубликованной версии пайплайна и ставит его в очередь
// супервизора.
func (h *StartRunHandler) Handle(ctx context.Context, in StartRun) (StartRunResult, error) {
	tenant := domain.TenantID(in.TenantID)
	version := in.PipelineVersion
	if version <= 0 {
		latest, err := h.pipelines.LatestPublished(ctx, domain.PipelineID(in.PipelineID), tenant)
		if err != nil {
			return StartRunResult{}, err
		}
		version = latest
	}
	pipeline, err := h.pipelines.FindByID(ctx, domain.PipelineID(in.PipelineID), version, tenant)
	if err != nil {
		return StartRunResult{}, err
	}
	if !pipeline.IsPublished() {
		return StartRunResult{}, errors.New("запуск агента: версия пайплайна не опубликована")
	}
	mission, err := domain.NewAgentMission(domain.MissionID(in.MissionID), domain.CompanyID(in.CompanyID), tenant, in.Criteria)
	if err != nil {
		return StartRunResult{}, err
	}
	runID := domain.RunID(in.RunID)
	if runID == "" {
		runID = h.ids.NewRunID()
	}
	run, err := domain.NewRun(domain.NewRunParams{
		ID:       runID,
		Mission:  mission,
		Pipeline: pipeline,
		At:       h.clock.Now(),
		Header:   header(h.ids, in.CorrelationID, in.CausationID),
	})
	if err != nil {
		return StartRunResult{}, err
	}
	if err := h.supervisor.Admit(ctx, run); err != nil {
		return StartRunResult{}, err
	}
	// Состояние после приёма супервизором: запуск либо исполняется, если предел
	// не достигнут, либо ждёт в очереди.
	status := domain.StatusQueued
	if committed, err := h.runs.FindByID(ctx, runID, tenant); err == nil {
		status = committed.Status()
	}
	return StartRunResult{RunID: string(runID), PipelineVersion: version, Status: status}, nil
}
