// Package query — прикладные сценарии чтения контекста agent-orchestration:
// наблюдаемое состояние запуска агента для контракта agent-run-state и экрана
// операций. Сценарии только читают агрегат и не меняют его.
package query

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

// RunStateQuery — вход чтения состояния запуска.
type RunStateQuery struct {
	RunID    string
	TenantID string
}

// RunStateHandler читает наблюдаемое состояние запуска агента.
type RunStateHandler struct {
	runs ports.RunRepository
}

// NewRunStateHandler собирает сценарий чтения состояния.
func NewRunStateHandler(runs ports.RunRepository) *RunStateHandler {
	return &RunStateHandler{runs: runs}
}

// Handle возвращает состояние запуска в форме контракта agent-run-state. Отсутствие
// запуска переводится в agentrunstate.ErrNotFound.
func (h *RunStateHandler) Handle(ctx context.Context, in RunStateQuery) (agentrunstate.RunState, error) {
	run, err := h.runs.FindByID(ctx, domain.RunID(in.RunID), domain.TenantID(in.TenantID))
	if err != nil {
		if errors.Is(err, ports.ErrRunNotFound) {
			return agentrunstate.RunState{}, agentrunstate.ErrNotFound
		}
		return agentrunstate.RunState{}, err
	}
	mission := run.Mission()
	state := agentrunstate.RunState{
		RunID:           string(run.ID()),
		TenantID:        string(run.TenantID()),
		MissionID:       string(mission.ID()),
		CompanyID:       string(mission.CompanyID()),
		PipelineID:      string(run.PipelineID()),
		PipelineVersion: run.PipelineVersion(),
		Status:          string(run.Status()),
		Cursor:          run.Cursor(),
	}
	steps := run.Steps()
	state.Steps = make([]agentrunstate.StepState, 0, len(steps))
	for _, step := range steps {
		state.Steps = append(state.Steps, agentrunstate.StepState{
			StepID:   string(step.StepID()),
			BlockRef: string(step.Ref()),
			Order:    step.Order(),
			Status:   string(step.Status()),
			Attempts: step.Attempts(),
			Outcome:  string(step.Outcome().Kind()),
			Reason:   step.Outcome().Reason(),
		})
	}
	if current, ok := run.CurrentStep(); ok {
		state.CurrentStep = string(current.StepID())
	}
	if approval := run.Approval(); approval != nil {
		state.AwaitingApproval = run.Status() == domain.StatusAwaitingApproval
		state.DraftRef = string(approval.DraftRef())
		state.DraftVersion = approval.DraftVersion()
	}
	return state, nil
}
