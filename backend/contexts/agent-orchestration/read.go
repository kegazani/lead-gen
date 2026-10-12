// Публичная поверхность корневого пакета для composition root: публикация версии
// пайплайна и read-поверхность запусков (контракт agent-run-state) для консоли
// операций.
//
// Read-поверхность отдаётся в форме опубликованного контракта agent-run-state: при
// co-location — прямым вызовом (InProc через RunReadModel), при разделении сервисов
// — сетевым клиентом (contracts/agent-orchestration/agentrunstate/httpport,
// design.md, Реш. 6).
package agentorchestration

import (
	"context"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/query"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

// PipelineStep — публичное описание шага публикуемой версии пайплайна.
type PipelineStep struct {
	// ID — идентификатор шага. Обязателен и уникален в версии.
	ID string
	// BlockRef — ссылка на зарегистрированный блок.
	BlockRef string
	// Params — параметры блока.
	Params map[string]string
	// MaxAttempts — предел попыток шага; 0 — значение по умолчанию.
	MaxAttempts int
}

// PublishPipelineRequest — вход публикации версии пайплайна из composition root.
type PublishPipelineRequest struct {
	PipelineID string
	TenantID   string
	// Version — номер версии; 0 — следующая после последней опубликованной.
	Version       int
	Steps         []PipelineStep
	Actor         string
	CorrelationID string
	CausationID   string
}

// PublishPipelineResult — наблюдаемый исход публикации.
type PublishPipelineResult struct {
	PipelineID string
	Version    int
}

// PublishPipeline публикует версию пайплайна: состав шагов проверяется против
// реестра объявленных блоков (Config.Blocks), неизвестный блок отвергается. Метод
// даёт composition root публичную точку входа, не раскрывая internal-типы команд.
func (s *Service) PublishPipeline(ctx context.Context, req PublishPipelineRequest) (PublishPipelineResult, error) {
	steps := make([]command.PipelineStepInput, 0, len(req.Steps))
	for _, step := range req.Steps {
		steps = append(steps, command.PipelineStepInput{
			ID:          step.ID,
			BlockRef:    step.BlockRef,
			Params:      step.Params,
			MaxAttempts: step.MaxAttempts,
		})
	}
	result, err := s.slice.PublishPipeline.Handle(ctx, command.PublishPipeline{
		PipelineID:    req.PipelineID,
		TenantID:      req.TenantID,
		Version:       req.Version,
		Steps:         steps,
		Actor:         req.Actor,
		CorrelationID: req.CorrelationID,
		CausationID:   req.CausationID,
	})
	if err != nil {
		return PublishPipelineResult{}, err
	}
	return PublishPipelineResult{PipelineID: result.PipelineID, Version: result.Version}, nil
}

// RunReadModel — read-поверхность запусков агента: состояние запуска (подробности
// шага) и список/лента запусков. Форма — опубликованный контракт agent-run-state.
type RunReadModel interface {
	agentrunstate.Reader
	agentrunstate.Lister
}

// RunReadModel возвращает read-поверхность запусков для консоли операций и
// внешних потребителей. В одном процессе вызов идёт напрямую (co-location).
func (s *Service) RunReadModel() RunReadModel {
	return serviceReadModel{slice: s.slice}
}

// serviceReadModel адаптирует прикладные сценарии чтения к опубликованному
// контракту agent-run-state.
type serviceReadModel struct {
	slice *Slice
}

// RunState реализует agentrunstate.Reader.
func (m serviceReadModel) RunState(ctx context.Context, request agentrunstate.Request) (agentrunstate.RunState, error) {
	return m.slice.RunState.Handle(ctx, query.RunStateQuery{
		RunID:    request.RunID,
		TenantID: request.TenantID,
	})
}

// List реализует agentrunstate.Lister.
func (m serviceReadModel) List(ctx context.Context, request agentrunstate.ListRequest) ([]agentrunstate.RunSummary, error) {
	return m.slice.ListRuns.Handle(ctx, query.ListRunsQuery{
		TenantID: request.TenantID,
		Status:   request.Status,
		Limit:    request.Limit,
	})
}

// Проверка соответствия read-поверхности опубликованному контракту на этапе
// компиляции.
var _ agentrunstate.ReadModel = serviceReadModel{}
