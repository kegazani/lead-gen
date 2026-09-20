package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// CreateCampaign — команда создания кампании.
//
// Идемпотентность опирается на естественный ключ кампании: CommandID — ключ
// границы контракта campaign-command, а CampaignID из команды позволяет
// распознать повтор и не создавать вторую кампанию. Соответствие
// (tenant_id, command_id) → campaign_id обеспечивает граница адаптера
// (задача 6.2); сам срез ключ не хранит.
type CreateCampaign struct {
	CommandID  domain.CommandID
	CampaignID domain.CampaignID
	TenantID   domain.TenantID
	Actor      domain.ActorID
	Name       string
	Window     domain.CampaignWindow
}

// CreateCampaignResult — результат создания кампании.
type CreateCampaignResult struct {
	CampaignID domain.CampaignID
	State      domain.CampaignState

	// Created равно false, если кампания уже существовала: повтор команды не
	// создаёт вторую и не меняет состояние.
	Created bool
}

// CreateCampaignHandler — use case создания кампании.
type CreateCampaignHandler struct {
	access ports.AccessDecision
	uow    ports.UnitOfWork
}

// NewCreateCampaignHandler собирает use case из портов доступа и транзакции.
func NewCreateCampaignHandler(access ports.AccessDecision, uow ports.UnitOfWork) *CreateCampaignHandler {
	return &CreateCampaignHandler{access: access, uow: uow}
}

// Handle проверяет право доступа, создаёт кампанию и сохраняет её вместе с
// событиями в одной транзакции. Доменных правил сценарий не содержит: состав
// кампании проверяет домен, повтор распознаётся по уже сохранённому агрегату.
func (h *CreateCampaignHandler) Handle(ctx context.Context, cmd CreateCampaign) (CreateCampaignResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return CreateCampaignResult{}, err
	}

	var result CreateCampaignResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		existing, err := tx.Campaigns().FindByID(ctx, cmd.CampaignID)
		switch {
		case err == nil:
			if existing == nil {
				return fmt.Errorf("загрузка кампании %s: %w", cmd.CampaignID, ports.ErrCampaignNotFound)
			}
			if existing.TenantID() != cmd.TenantID {
				return fmt.Errorf("%w: кампания %s", ErrCampaignTenantMismatch, cmd.CampaignID)
			}
			result = CreateCampaignResult{
				CampaignID: existing.ID(),
				State:      existing.State(),
				Created:    false,
			}
			return nil
		case !isNotFound(err):
			return fmt.Errorf("загрузка кампании %s: %w", cmd.CampaignID, err)
		}

		campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
			ID:       cmd.CampaignID,
			TenantID: cmd.TenantID,
			Name:     cmd.Name,
			Window:   cmd.Window,
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

		result = CreateCampaignResult{
			CampaignID: campaign.ID(),
			State:      campaign.State(),
			Created:    true,
		}
		return nil
	})
	if err != nil {
		return CreateCampaignResult{}, err
	}
	return result, nil
}
