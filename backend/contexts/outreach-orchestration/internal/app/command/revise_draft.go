package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// ReviseDraft — команда доработки черновика. Доработка повышает версию и
// возвращает черновик на утверждение; прежнее решение к новой версии не
// относится (инвариант I2).
type ReviseDraft struct {
	CampaignID      domain.CampaignID
	TenantID        domain.TenantID
	Actor           domain.ActorID
	DraftID         domain.DraftID
	ExpectedVersion int
	Spec            domain.DraftSpec
}

// ReviseDraftResult — результат доработки черновика.
type ReviseDraftResult struct {
	CampaignID domain.CampaignID
	DraftID    domain.DraftID
	Version    int
	State      domain.DraftState

	// Revised равно false, если доработка уже применена: повтор команды с той
	// же ожидаемой версией и тем же составом идемпотентен.
	Revised bool
}

// ReviseDraftHandler — use case доработки черновика. Время порт Clock не
// нужен: доработка не создаёт решения с отметкой времени.
type ReviseDraftHandler struct {
	access ports.AccessDecision
	uow    ports.UnitOfWork
}

// NewReviseDraftHandler собирает use case из портов доступа и транзакции.
func NewReviseDraftHandler(access ports.AccessDecision, uow ports.UnitOfWork) *ReviseDraftHandler {
	return &ReviseDraftHandler{access: access, uow: uow}
}

// Handle применяет доработку. Идемпотентность распознаётся по паре
// (draft_id, draft_version): если текущая версия уже на единицу больше
// ожидаемой и состав совпадает, повторная команда ничего не меняет.
func (h *ReviseDraftHandler) Handle(ctx context.Context, cmd ReviseDraft) (ReviseDraftResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return ReviseDraftResult{}, err
	}

	var result ReviseDraftResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}

		if existing, ok := campaign.Draft(cmd.DraftID); ok &&
			existing.Version() == cmd.ExpectedVersion+1 &&
			existing.State() == domain.DraftStatePendingApproval &&
			existing.Spec() == cmd.Spec {
			result = ReviseDraftResult{
				CampaignID: campaign.ID(),
				DraftID:    existing.ID(),
				Version:    existing.Version(),
				State:      existing.State(),
				Revised:    false,
			}
			return nil
		}

		draft, err := campaign.ReviseDraft(cmd.DraftID, cmd.ExpectedVersion, cmd.Spec)
		if err != nil {
			return err
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = ReviseDraftResult{
			CampaignID: campaign.ID(),
			DraftID:    draft.ID(),
			Version:    draft.Version(),
			State:      draft.State(),
			Revised:    true,
		}
		return nil
	})
	if err != nil {
		return ReviseDraftResult{}, err
	}
	return result, nil
}
