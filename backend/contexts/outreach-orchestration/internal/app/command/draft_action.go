package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// DraftAction — команда постановки черновика действия: материал, сегмент,
// адресат и канал.
type DraftAction struct {
	CampaignID domain.CampaignID
	TenantID   domain.TenantID
	Actor      domain.ActorID
	DraftID    domain.DraftID
	Spec       domain.DraftSpec
}

// DraftActionResult — результат постановки черновика.
type DraftActionResult struct {
	CampaignID domain.CampaignID
	DraftID    domain.DraftID
	Version    int
	State      domain.DraftState

	// Created равно false, если черновик с таким идентификатором и тем же
	// составом уже поставлен: повтор команды не создаёт второй черновик.
	Created bool
}

// DraftActionHandler — use case постановки черновика.
type DraftActionHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
}

// NewDraftActionHandler собирает use case из портов доступа, времени и
// транзакции.
func NewDraftActionHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *DraftActionHandler {
	return &DraftActionHandler{access: access, clock: clock, uow: uow}
}

// Handle ставит черновик в кампанию. Состояние перехода («ожидает
// утверждения») определяет агрегат; прикладной слой только сохраняет агрегат и
// его события в одной транзакции. Исполнение без решения оператора невозможно:
// постановка черновика не порождает action-approved.
func (h *DraftActionHandler) Handle(ctx context.Context, cmd DraftAction) (DraftActionResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return DraftActionResult{}, err
	}

	var result DraftActionResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}

		if existing, ok := campaign.Draft(cmd.DraftID); ok {
			if existing.Spec() != cmd.Spec {
				return fmt.Errorf("%w: черновик %s", domain.ErrDuplicateDraft, cmd.DraftID)
			}
			result = DraftActionResult{
				CampaignID: campaign.ID(),
				DraftID:    existing.ID(),
				Version:    existing.Version(),
				State:      existing.State(),
				Created:    false,
			}
			return nil
		}

		draft, _, err := campaign.DraftAction(cmd.DraftID, cmd.Spec, h.clock.Now())
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

		result = DraftActionResult{
			CampaignID: campaign.ID(),
			DraftID:    draft.ID(),
			Version:    draft.Version(),
			State:      draft.State(),
			Created:    true,
		}
		return nil
	})
	if err != nil {
		return DraftActionResult{}, err
	}
	return result, nil
}
