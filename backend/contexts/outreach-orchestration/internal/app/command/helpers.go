package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// DraftDecisionResult — исход решения оператора по черновику.
type DraftDecisionResult struct {
	DecisionID   domain.DecisionID
	CampaignID   domain.CampaignID
	DraftID      domain.DraftID
	DraftVersion int
	Actor        domain.ActorID
	Outcome      domain.DecisionOutcome
	DecidedAt    time.Time

	// Recorded равно false, если решение по паре (draft_id, draft_version) уже
	// было принято ранее: повтор команды идемпотентен и второй записи не создаёт.
	Recorded bool
}

// decisionDeps — общие зависимости сценариев решения. Транзакцию и проверку
// доступа задаёт прикладной слой; допустимость решения определяет агрегат.
type decisionDeps struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
}

// decisionRequest — общие данные команды решения.
type decisionRequest struct {
	CampaignID      domain.CampaignID
	TenantID        domain.TenantID
	Actor           domain.ActorID
	DraftID         domain.DraftID
	DecisionID      domain.DecisionID
	ExpectedVersion int
	Reason          string
}

// decideOnCampaign — метод агрегата, фиксирующий решение.
type decideOnCampaign func(*domain.Campaign, domain.DecisionParams) (domain.OperatorDecision, []domain.DomainEvent, error)

// handleDecision выполняет общий путь решения: проверка доступа, транзакция,
// вызов метода агрегата, сохранение агрегата и запись событий в outbox.
//
// Идемпотентность определяет домен по паре (draft_id, draft_version): если
// решение уже принято, агрегат возвращает существующее и не порождает событий,
// поэтому прикладной слой лишь отражает это в Recorded.
func handleDecision(
	ctx context.Context,
	deps decisionDeps,
	req decisionRequest,
	decide decideOnCampaign,
) (DraftDecisionResult, error) {
	if err := authorize(ctx, deps.access, req.Actor, req.TenantID); err != nil {
		return DraftDecisionResult{}, err
	}

	var result DraftDecisionResult
	err := deps.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := rejectIfCampaignCancelled(ctx, tx, req.TenantID.String(), req.CampaignID.String()); err != nil {
			return err
		}
		campaign, err := loadCampaign(ctx, tx.Campaigns(), req.CampaignID, req.TenantID)
		if err != nil {
			return err
		}

		alreadyDecided := false
		for _, existing := range campaign.DecisionsForDraft(req.DraftID) {
			if existing.DraftVersion() == req.ExpectedVersion {
				alreadyDecided = true
				break
			}
		}

		decision, _, err := decide(campaign, domain.DecisionParams{
			DecisionID:   req.DecisionID,
			DraftID:      req.DraftID,
			DraftVersion: req.ExpectedVersion,
			Actor:        req.Actor,
			Reason:       req.Reason,
			DecidedAt:    deps.clock.Now(),
		})
		if err != nil {
			return err
		}

		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", req.CampaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", req.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = DraftDecisionResult{
			DecisionID:   decision.ID(),
			CampaignID:   decision.CampaignID(),
			DraftID:      decision.DraftID(),
			DraftVersion: decision.DraftVersion(),
			Actor:        decision.Actor(),
			Outcome:      decision.Outcome(),
			DecidedAt:    decision.DecidedAt(),
			Recorded:     !alreadyDecided,
		}
		return nil
	})
	if err != nil {
		return DraftDecisionResult{}, err
	}
	return result, nil
}

// isNotFound сообщает, что ошибка означает отсутствие агрегата в хранилище.
func isNotFound(err error) bool {
	return errors.Is(err, ports.ErrCampaignNotFound)
}
