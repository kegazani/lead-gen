package command

import (
	"context"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// RejectDraft — команда отклонения черновика оператором. Отклонение требует
// причины; это правило домена, и прикладной слой его не повторяет.
type RejectDraft struct {
	CampaignID      domain.CampaignID
	TenantID        domain.TenantID
	Actor           domain.ActorID
	DraftID         domain.DraftID
	DecisionID      domain.DecisionID
	ExpectedVersion int
	Reason          string
}

// RejectDraftHandler — use case отклонения черновика.
type RejectDraftHandler struct {
	deps decisionDeps
}

// NewRejectDraftHandler собирает use case из портов доступа, времени и
// транзакции.
func NewRejectDraftHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *RejectDraftHandler {
	return &RejectDraftHandler{deps: decisionDeps{access: access, clock: clock, uow: uow}}
}

// Handle фиксирует отклонение. Событие action-approved не порождается, поэтому
// outbox остаётся пустым, а черновик возвращается на доработку.
func (h *RejectDraftHandler) Handle(ctx context.Context, cmd RejectDraft) (DraftDecisionResult, error) {
	return handleDecision(ctx, h.deps, decisionRequest{
		CampaignID:      cmd.CampaignID,
		TenantID:        cmd.TenantID,
		Actor:           cmd.Actor,
		DraftID:         cmd.DraftID,
		DecisionID:      cmd.DecisionID,
		ExpectedVersion: cmd.ExpectedVersion,
		Reason:          cmd.Reason,
	}, (*domain.Campaign).RejectDraft)
}
