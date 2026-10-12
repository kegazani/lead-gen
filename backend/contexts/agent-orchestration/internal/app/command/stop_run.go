package command

import (
	"context"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/supervisor"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
)

// StopRun — вход команды остановки агента.
type StopRun struct {
	RunID         string
	TenantID      string
	Actor         string
	Reason        string
	CorrelationID string
	CausationID   string
}

// StopRunHandler останавливает агента командой оператора: терминальное состояние
// «остановлен» и команда остановки рантайму.
type StopRunHandler struct {
	supervisor *supervisor.Supervisor
}

// NewStopRunHandler собирает сценарий остановки агента.
func NewStopRunHandler(sup *supervisor.Supervisor) *StopRunHandler {
	return &StopRunHandler{supervisor: sup}
}

// Handle выполняет остановку.
func (h *StopRunHandler) Handle(ctx context.Context, in StopRun) error {
	return h.supervisor.Stop(ctx, domain.RunID(in.RunID), domain.TenantID(in.TenantID), domain.ActorID(in.Actor), in.Reason, in.CorrelationID)
}
