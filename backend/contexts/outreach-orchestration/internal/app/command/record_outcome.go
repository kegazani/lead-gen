package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// RecordOutcome — команда записи реакции лида по участию в цепочке.
type RecordOutcome struct {
	CampaignID  domain.CampaignID
	TenantID    domain.TenantID
	Actor       domain.ActorID
	ExecutionID domain.ExecutionID
	Reaction    domain.Reaction

	// Confident равно false, если классификация ответа неуверенна: ветка
	// автоматически не выбирается, решение принимает оператор.
	Confident bool
	Reason    string
}

// RecordOutcomeResult — результат записи реакции.
type RecordOutcomeResult struct {
	CampaignID  domain.CampaignID
	ExecutionID domain.ExecutionID
	State       domain.ExecutionState
}

// RecordOutcomeHandler — use case записи реакции лида.
type RecordOutcomeHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
}

// NewRecordOutcomeHandler собирает use case записи реакции.
func NewRecordOutcomeHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *RecordOutcomeHandler {
	return &RecordOutcomeHandler{access: access, clock: clock, uow: uow}
}

// Handle фиксирует реакцию: приостанавливает, останавливает или записывает
// наблюдение. Ничего не отправляет.
func (h *RecordOutcomeHandler) Handle(ctx context.Context, cmd RecordOutcome) (RecordOutcomeResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return RecordOutcomeResult{}, err
	}

	var result RecordOutcomeResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}
		if _, ok := campaign.Execution(cmd.ExecutionID); !ok {
			return fmt.Errorf("%w: участие %s", domain.ErrExecutionNotFound, cmd.ExecutionID)
		}
		if err := campaign.RecordReaction(domain.RecordReactionParams{
			ExecutionID: cmd.ExecutionID,
			Reaction:    cmd.Reaction,
			Confident:   cmd.Confident,
			At:          h.clock.Now(),
			Reason:      cmd.Reason,
		}); err != nil {
			return err
		}
		execution, _ := campaign.Execution(cmd.ExecutionID)
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = RecordOutcomeResult{
			CampaignID:  campaign.ID(),
			ExecutionID: execution.ID(),
			State:       execution.State(),
		}
		return nil
	})
	if err != nil {
		return RecordOutcomeResult{}, err
	}
	return result, nil
}

// ResumeExecution — команда возобновления приостановленной цепочки оператором.
type ResumeExecution struct {
	CampaignID  domain.CampaignID
	TenantID    domain.TenantID
	Actor       domain.ActorID
	ExecutionID domain.ExecutionID
}

// ResumeExecutionResult — результат возобновления.
type ResumeExecutionResult struct {
	CampaignID  domain.CampaignID
	ExecutionID domain.ExecutionID
	State       domain.ExecutionState
	Resumed     bool
}

// ResumeExecutionHandler — use case возобновления цепочки.
type ResumeExecutionHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
}

// NewResumeExecutionHandler собирает use case возобновления.
func NewResumeExecutionHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *ResumeExecutionHandler {
	return &ResumeExecutionHandler{access: access, clock: clock, uow: uow}
}

// Handle возобновляет приостановленную цепочку. Автоматического возобновления
// нет: команду подаёт оператор.
func (h *ResumeExecutionHandler) Handle(ctx context.Context, cmd ResumeExecution) (ResumeExecutionResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return ResumeExecutionResult{}, err
	}

	var result ResumeExecutionResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}
		if _, ok := campaign.Execution(cmd.ExecutionID); !ok {
			return fmt.Errorf("%w: участие %s", domain.ErrExecutionNotFound, cmd.ExecutionID)
		}
		if err := campaign.ResumeExecution(cmd.ExecutionID, cmd.Actor, h.clock.Now()); err != nil {
			return err
		}
		execution, _ := campaign.Execution(cmd.ExecutionID)
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = ResumeExecutionResult{
			CampaignID:  campaign.ID(),
			ExecutionID: execution.ID(),
			State:       execution.State(),
			Resumed:     true,
		}
		return nil
	})
	if err != nil {
		return ResumeExecutionResult{}, err
	}
	return result, nil
}
