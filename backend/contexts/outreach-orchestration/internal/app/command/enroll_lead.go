package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// EnrollLead — команда ручного добавления лида в цепочку.
type EnrollLead struct {
	CampaignID  domain.CampaignID
	TenantID    domain.TenantID
	Actor       domain.ActorID
	SequenceID  domain.SequenceID
	ExecutionID domain.ExecutionID
	Lead        domain.LeadID
	Recipient   domain.RecipientID
}

// EnrollLeadResult — результат добавления лида.
type EnrollLeadResult struct {
	CampaignID  domain.CampaignID
	ExecutionID domain.ExecutionID
	State       domain.ExecutionState

	// DraftID непусто, если первый черновик шага сформирован сразу: задержка
	// первого шага уже прошла. Иначе черновик появится на очередном проходе
	// движка.
	DraftID domain.DraftID

	// Created равно false, если участие с таким идентификатором уже существует.
	Created bool
}

// EnrollLeadHandler — use case добавления лида в цепочку.
type EnrollLeadHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
	limits domain.ParticipationLimits
}

// NewEnrollLeadHandler собирает use case. Ограничения участия задаёт композиция:
// это конфигурация, а не правило одного сценария.
func NewEnrollLeadHandler(
	access ports.AccessDecision,
	clock ports.Clock,
	uow ports.UnitOfWork,
	limits domain.ParticipationLimits,
) *EnrollLeadHandler {
	return &EnrollLeadHandler{access: access, clock: clock, uow: uow, limits: limits}
}

// Handle добавляет лида и сразу выполняет проход движка, чтобы первый шаг был
// сформирован по правилам сценария (с учётом задержки и ограничений).
func (h *EnrollLeadHandler) Handle(ctx context.Context, cmd EnrollLead) (EnrollLeadResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return EnrollLeadResult{}, err
	}

	var result EnrollLeadResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}

		if existing, ok := campaign.Execution(cmd.ExecutionID); ok {
			result = EnrollLeadResult{
				CampaignID:  campaign.ID(),
				ExecutionID: existing.ID(),
				State:       existing.State(),
				DraftID:     existing.DraftID(),
				Created:     false,
			}
			return nil
		}

		execution, err := campaign.EnrollLead(domain.EnrollmentParams{
			ExecutionID: cmd.ExecutionID,
			SequenceID:  cmd.SequenceID,
			TenantID:    cmd.TenantID,
			Lead:        cmd.Lead,
			Recipient:   cmd.Recipient,
			At:          h.clock.Now(),
		})
		if err != nil {
			return err
		}
		if _, err := campaign.AdvanceSequences(h.clock.Now(), h.limits); err != nil {
			return err
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = EnrollLeadResult{
			CampaignID:  campaign.ID(),
			ExecutionID: execution.ID(),
			State:       execution.State(),
			DraftID:     execution.DraftID(),
			Created:     true,
		}
		return nil
	})
	if err != nil {
		return EnrollLeadResult{}, err
	}
	return result, nil
}

// RemoveLead — команда ручного удаления лида из цепочки.
type RemoveLead struct {
	CampaignID  domain.CampaignID
	TenantID    domain.TenantID
	Actor       domain.ActorID
	ExecutionID domain.ExecutionID
	Reason      string
}

// RemoveLeadResult — результат удаления лида.
type RemoveLeadResult struct {
	CampaignID  domain.CampaignID
	ExecutionID domain.ExecutionID
	State       domain.ExecutionState
	Removed     bool
}

// RemoveLeadHandler — use case удаления лида из цепочки.
type RemoveLeadHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
}

// NewRemoveLeadHandler собирает use case удаления лида.
func NewRemoveLeadHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *RemoveLeadHandler {
	return &RemoveLeadHandler{access: access, clock: clock, uow: uow}
}

// Handle прекращает цепочку лида и фиксирует причину в журнале. Последующие шаги
// не формируются.
func (h *RemoveLeadHandler) Handle(ctx context.Context, cmd RemoveLead) (RemoveLeadResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return RemoveLeadResult{}, err
	}

	var result RemoveLeadResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}
		execution, ok := campaign.Execution(cmd.ExecutionID)
		if !ok {
			return fmt.Errorf("%w: участие %s", domain.ErrExecutionNotFound, cmd.ExecutionID)
		}
		if err := campaign.RemoveLead(cmd.ExecutionID, cmd.Actor, cmd.Reason, h.clock.Now()); err != nil {
			return err
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = RemoveLeadResult{
			CampaignID:  campaign.ID(),
			ExecutionID: execution.ID(),
			State:       execution.State(),
			Removed:     true,
		}
		return nil
	})
	if err != nil {
		return RemoveLeadResult{}, err
	}
	return result, nil
}
