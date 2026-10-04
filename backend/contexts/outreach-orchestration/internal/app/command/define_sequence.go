package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// DefineSequence — команда создания сценария цепочки: шаги, типы действий,
// задержки и условия перехода.
type DefineSequence struct {
	CampaignID domain.CampaignID
	TenantID   domain.TenantID
	Actor      domain.ActorID
	SequenceID domain.SequenceID
	Name       string
	Steps      []domain.SequenceStep
}

// DefineSequenceResult — результат создания сценария.
type DefineSequenceResult struct {
	CampaignID domain.CampaignID
	SequenceID domain.SequenceID
	Steps      int

	// Created равно false, если сценарий с таким идентификатором и тем же
	// составом уже создан: повтор команды не создаёт второй сценарий.
	Created bool
}

// DefineSequenceHandler — use case создания сценария.
type DefineSequenceHandler struct {
	access ports.AccessDecision
	uow    ports.UnitOfWork
}

// NewDefineSequenceHandler собирает use case из порта доступа и транзакции.
func NewDefineSequenceHandler(access ports.AccessDecision, uow ports.UnitOfWork) *DefineSequenceHandler {
	return &DefineSequenceHandler{access: access, uow: uow}
}

// Handle создаёт сценарий и сохраняет его вместе с событиями в одной
// транзакции. Сценарий неизменяем: повтор с другим составом конфликтует, повтор
// с тем же составом идемпотентен.
func (h *DefineSequenceHandler) Handle(ctx context.Context, cmd DefineSequence) (DefineSequenceResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return DefineSequenceResult{}, err
	}

	var result DefineSequenceResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}

		if existing, ok := campaign.SequenceDefinition(cmd.SequenceID); ok {
			if existing.Name() != cmd.Name || !stepsEqual(existing.Steps(), cmd.Steps) {
				return fmt.Errorf("%w: сценарий %s", domain.ErrDuplicateSequence, cmd.SequenceID)
			}
			result = DefineSequenceResult{
				CampaignID: campaign.ID(),
				SequenceID: existing.ID(),
				Steps:      existing.Len(),
				Created:    false,
			}
			return nil
		}

		definition, err := campaign.DefineSequence(domain.SequenceDefinitionParams{
			ID:       cmd.SequenceID,
			Campaign: cmd.CampaignID,
			Tenant:   cmd.TenantID,
			Name:     cmd.Name,
			Steps:    cmd.Steps,
		})
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

		result = DefineSequenceResult{
			CampaignID: campaign.ID(),
			SequenceID: definition.ID(),
			Steps:      definition.Len(),
			Created:    true,
		}
		return nil
	})
	if err != nil {
		return DefineSequenceResult{}, err
	}
	return result, nil
}

// stepsEqual сравнивает составы шагов. Шаг сценария — сравнимое значение, поэтому
// сравнение не требует доступа к закрытым полям.
func stepsEqual(a, b []domain.SequenceStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
