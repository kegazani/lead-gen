package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// ChangeDealStage — команда перехода этапа сделки.
type ChangeDealStage struct {
	DealID   domain.DealID
	TenantID domain.TenantID
	Actor    domain.ActorID

	// To — целевой этап согласованного жизненного цикла.
	To domain.DealStage

	Reason        string
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// ChangeDealStageHandler — use case перехода этапа.
type ChangeDealStageHandler struct{ base }

// NewChangeDealStageHandler собирает use case.
func NewChangeDealStageHandler(
	deals ports.DealRepository,
	syncs ports.CRMSyncRepository,
	uow ports.UnitOfWork,
	clock ports.Clock,
	ids ports.IDGenerator,
) *ChangeDealStageHandler {
	return &ChangeDealStageHandler{base{deals: deals, syncs: syncs, uow: uow, clock: clock, ids: ids}}
}

// Handle переводит сделку на новый этап в одной транзакции с записью события
// deal-state-changed. Переход проверяет aggregate root по согласованному
// workflow.
func (h *ChangeDealStageHandler) Handle(ctx context.Context, cmd ChangeDealStage) (DealResult, error) {
	deal, err := h.loadDeal(ctx, cmd.DealID, cmd.TenantID)
	if err != nil {
		return DealResult{}, err
	}
	expected := deal.Version()
	in := h.actionInput(cmd.Actor, cmd.At, cmd.Reason, cmd.CorrelationID, cmd.CausationID)
	if _, err := deal.ChangeStage(in, cmd.To); err != nil {
		return DealResult{}, err
	}
	events, err := h.commit(ctx, deal, expected, nil)
	if err != nil {
		return DealResult{}, err
	}
	return DealResult{
		DealID:  deal.ID(),
		Stage:   deal.Stage(),
		Version: deal.Version(),
		Events:  events,
	}, nil
}
