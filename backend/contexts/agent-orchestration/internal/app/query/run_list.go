package query

import (
	"context"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

// ListRunsQuery — вход чтения списка (ленты) запусков тенанта.
type ListRunsQuery struct {
	// TenantID — тенант-владелец запусков. Обязателен.
	TenantID string
	// Status — фильтр по статусу запуска; пусто — все статусы.
	Status string
	// Limit — предел числа строк; <= 0 — без предела.
	Limit int
}

// ListRunsHandler читает список (ленту) запусков тенанта в форме контракта
// agent-run-state. Сценарий только читает агрегат и не меняет его.
type ListRunsHandler struct {
	runs ports.RunRepository
}

// NewListRunsHandler собирает сценарий чтения ленты.
func NewListRunsHandler(runs ports.RunRepository) *ListRunsHandler {
	return &ListRunsHandler{runs: runs}
}

// Handle возвращает сводки запусков тенанта по фильтру. Тенант обязателен:
// выборка без него отвергается портом (инвариант тенант-скоупа), а не отдаётся
// пустым списком.
func (h *ListRunsHandler) Handle(ctx context.Context, in ListRunsQuery) ([]agentrunstate.RunSummary, error) {
	runs, err := h.runs.List(ctx, ports.RunFilter{
		Tenant: domain.TenantID(in.TenantID),
		Status: domain.RunStatus(in.Status),
		Limit:  in.Limit,
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]agentrunstate.RunSummary, 0, len(runs))
	for _, run := range runs {
		summary := agentrunstate.RunSummary{
			RunID:            string(run.ID()),
			TenantID:         string(run.TenantID()),
			MissionID:        string(run.Mission().ID()),
			CompanyID:        string(run.Mission().CompanyID()),
			PipelineID:       string(run.PipelineID()),
			PipelineVersion:  run.PipelineVersion(),
			Status:           string(run.Status()),
			Cursor:           run.Cursor(),
			AwaitingApproval: run.Status() == domain.StatusAwaitingApproval,
			CreatedAt:        run.CreatedAt(),
			UpdatedAt:        run.UpdatedAt(),
		}
		if current, ok := run.CurrentStep(); ok {
			summary.CurrentStep = string(current.StepID())
			summary.CurrentBlock = string(current.Ref())
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}
