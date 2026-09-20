package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// ErrApprovalDeadlineRequired — срок утверждения не задан. Истечение срока без
// дедлайна не определено.
var ErrApprovalDeadlineRequired = errors.New("приложение: срок утверждения не задан")

// ExpireApproval — команда истечения срока утверждения черновика. Срок
// сверяется с временем порта Clock, поэтому тест управляет сроком.
type ExpireApproval struct {
	CampaignID      domain.CampaignID
	TenantID        domain.TenantID
	Actor           domain.ActorID
	DraftID         domain.DraftID
	ExpectedVersion int

	// Deadline — момент, после которого черновик считается просроченным.
	Deadline time.Time
}

// ExpireApprovalResult — результат истечения срока.
type ExpireApprovalResult struct {
	CampaignID   domain.CampaignID
	DraftID      domain.DraftID
	DraftVersion int
	State        domain.DraftState

	// Expired равно true, если срок истёк и черновик помечен просроченным.
	Expired bool

	// Notify равно true, если черновик помечен для уведомления инициатора.
	// Уведомление не является автоутверждением: черновик остаётся неисполненным.
	Notify bool
}

// ExpireApprovalHandler — use case истечения срока утверждения.
type ExpireApprovalHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
}

// NewExpireApprovalHandler собирает use case из портов доступа, времени и
// транзакции.
func NewExpireApprovalHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *ExpireApprovalHandler {
	return &ExpireApprovalHandler{access: access, clock: clock, uow: uow}
}

// Handle помечает черновик просроченным, если срок истёк. Автоутверждение
// запрещено: домен переводит черновик в состояние «срок истёк», а не
// «утверждено», поэтому исполнение остаётся невозможным. Если срок не истёк,
// состояние не меняется и результат сообщает Expired=false.
//
// Признак Notify — сигнал прикладного слоя о необходимости уведомления; домен
// отдельного события истечения не порождает, поэтому в outbox идут только
// события, произведённые агрегатом.
func (h *ExpireApprovalHandler) Handle(ctx context.Context, cmd ExpireApproval) (ExpireApprovalResult, error) {
	if cmd.Deadline.IsZero() {
		return ExpireApprovalResult{}, fmt.Errorf("%w: черновик %s", ErrApprovalDeadlineRequired, cmd.DraftID)
	}
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return ExpireApprovalResult{}, err
	}

	now := h.clock.Now()
	var result ExpireApprovalResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}
		draft, ok := campaign.Draft(cmd.DraftID)
		if !ok {
			return fmt.Errorf("%w: %s", domain.ErrDraftNotFound, cmd.DraftID)
		}
		if now.Before(cmd.Deadline) {
			result = ExpireApprovalResult{
				CampaignID:   campaign.ID(),
				DraftID:      draft.ID(),
				DraftVersion: draft.Version(),
				State:        draft.State(),
				Expired:      false,
				Notify:       false,
			}
			return nil
		}

		if err := campaign.ExpireDraft(cmd.DraftID, cmd.ExpectedVersion); err != nil {
			return err
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = ExpireApprovalResult{
			CampaignID:   campaign.ID(),
			DraftID:      draft.ID(),
			DraftVersion: draft.Version(),
			State:        draft.State(),
			Expired:      true,
			Notify:       true,
		}
		return nil
	})
	if err != nil {
		return ExpireApprovalResult{}, err
	}
	return result, nil
}
