package command

import (
	"context"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// ApproveDraft — команда утверждения черновика оператором.
//
// Ожидаемая версия черновика — естественный ключ идемпотентности и защита I2:
// решение принимается только по точной версии, а повтор не создаёт вторую
// запись решения (design.md, Решение 5).
type ApproveDraft struct {
	CampaignID      domain.CampaignID
	TenantID        domain.TenantID
	Actor           domain.ActorID
	DraftID         domain.DraftID
	DecisionID      domain.DecisionID
	ExpectedVersion int
	Reason          string
}

// ApproveDraftHandler — use case утверждения черновика.
type ApproveDraftHandler struct {
	deps decisionDeps
}

// NewApproveDraftHandler собирает use case из портов доступа, времени и
// транзакции.
func NewApproveDraftHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *ApproveDraftHandler {
	return &ApproveDraftHandler{deps: decisionDeps{access: access, clock: clock, uow: uow}}
}

// Handle фиксирует положительное решение и записывает action-approved в outbox
// в одной транзакции с изменением агрегата. Допустимость перехода решает
// агрегат; прикладной слой не дублирует инвариант I1.
func (h *ApproveDraftHandler) Handle(ctx context.Context, cmd ApproveDraft) (DraftDecisionResult, error) {
	return handleDecision(ctx, h.deps, decisionRequest{
		CampaignID:      cmd.CampaignID,
		TenantID:        cmd.TenantID,
		Actor:           cmd.Actor,
		DraftID:         cmd.DraftID,
		DecisionID:      cmd.DecisionID,
		ExpectedVersion: cmd.ExpectedVersion,
		Reason:          cmd.Reason,
	}, (*domain.Campaign).ApproveDraft)
}
