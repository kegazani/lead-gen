package query

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ExecutionState — durable read model; не зависит от Temporal runtime.
type ExecutionState struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
}

type ExecutionStateView struct {
	RequestID             domain.RequestID
	WorkflowID            string
	AttemptID             string
	CommandID             domain.CommandID
	Generation            int64
	State                 string
	Target                domain.LeadTarget
	ExecutionHistoryKnown bool
	HasAttempt            bool
}

type ExecutionStateHandler struct {
	attempts ports.ExecutionAttemptRepository
}

func NewExecutionStateHandler(attempts ports.ExecutionAttemptRepository) *ExecutionStateHandler {
	return &ExecutionStateHandler{attempts: attempts}
}

func (h *ExecutionStateHandler) Handle(ctx context.Context, q ExecutionState) (ExecutionStateView, error) {
	state, err := h.attempts.ReadOwnerState(ctx, q.TenantID, q.RequestID)
	if err != nil {
		return ExecutionStateView{}, err
	}
	stateName := string(state.Runtime)
	if !state.HasAttempt && state.ExecutionHistoryKnown {
		stateName = "not-admitted"
	}
	if !state.HasAttempt && !state.ExecutionHistoryKnown {
		stateName = "unknownLegacy"
	}
	view := ExecutionStateView{
		RequestID: q.RequestID, WorkflowID: "fulfillment-" + string(q.RequestID),
		State: stateName, Target: state.Target, ExecutionHistoryKnown: state.ExecutionHistoryKnown,
		HasAttempt: state.HasAttempt,
	}
	if state.Attempt != nil {
		view.AttemptID = state.Attempt.AttemptID
		view.CommandID = state.Attempt.Intent.CommandID()
		view.Generation = state.Attempt.Intent.Generation()
		view.State = string(state.Attempt.State)
	}
	return view, nil
}
