package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// PostponeDraft — команда отложения решения по черновику до указанного момента.
//
// Отложение не является решением: черновик остаётся «ожидает утверждения» и не
// исполняется. Срок обязателен: отложение без срока не определено.
type PostponeDraft struct {
	CampaignID      domain.CampaignID
	TenantID        domain.TenantID
	Actor           domain.ActorID
	DraftID         domain.DraftID
	ExpectedVersion int

	// Until — момент, до которого решение отложено. Нулевое значение
	// отвергается доменом.
	Until time.Time
}

// PostponeDraftResult — результат отложения.
type PostponeDraftResult struct {
	CampaignID     domain.CampaignID
	DraftID        domain.DraftID
	DraftVersion   int
	State          domain.DraftState
	PostponedUntil time.Time
}

// PostponeDraftHandler — use case отложения решения.
type PostponeDraftHandler struct {
	access ports.AccessDecision
	uow    ports.UnitOfWork
}

// NewPostponeDraftHandler собирает use case из портов доступа и транзакции.
func NewPostponeDraftHandler(access ports.AccessDecision, uow ports.UnitOfWork) *PostponeDraftHandler {
	return &PostponeDraftHandler{access: access, uow: uow}
}

// Handle откладывает решение. Право проверяет прикладной слой, допустимость
// перехода — агрегат. Отложение не порождает ни решения, ни доменного события:
// в outbox ничего не пишется, а черновик остаётся неисполненным.
func (h *PostponeDraftHandler) Handle(ctx context.Context, cmd PostponeDraft) (PostponeDraftResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return PostponeDraftResult{}, err
	}

	var result PostponeDraftResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}
		if err := campaign.PostponeDraft(cmd.DraftID, cmd.ExpectedVersion, cmd.Until); err != nil {
			return err
		}
		draft, ok := campaign.Draft(cmd.DraftID)
		if !ok {
			return fmt.Errorf("%w: %s", domain.ErrDraftNotFound, cmd.DraftID)
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = PostponeDraftResult{
			CampaignID:     campaign.ID(),
			DraftID:        draft.ID(),
			DraftVersion:   draft.Version(),
			State:          draft.State(),
			PostponedUntil: draft.PostponedUntil(),
		}
		return nil
	})
	if err != nil {
		return PostponeDraftResult{}, err
	}
	return result, nil
}
