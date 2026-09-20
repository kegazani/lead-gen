// Package command содержит прикладные сценарии изменения состояния контекста
// outreach-orchestration: создание кампании, постановку черновика, решения
// оператора и истечение срока утверждения.
//
// Слой задаёт транзакцию и проверку доступа, но не содержит доменных правил:
// решения о допустимости принимает только агрегат campaign. Изменение агрегата
// и запись его domain events в outbox выполняются в одной транзакции
// unit-of-work (transactional outbox, docs/domain-model.md).
package command

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

var (
	// ErrAccessDenied — актору отказано в праве действовать в тенанте.
	// Ошибка прикладного слоя: порт AccessDecision вернул отрицательное решение,
	// а не ошибку инфраструктуры.
	ErrAccessDenied = errors.New("приложение: нет права действовать в тенанте")

	// ErrCampaignTenantMismatch — кампания существует, но принадлежит другому
	// тенанту. Команда не пересекает границу тенанта.
	ErrCampaignTenantMismatch = errors.New("приложение: кампания принадлежит другому тенанту")
)

// authorize проверяет право актора действовать в тенанте. Доменных решений
// здесь нет: результат целиком определяется портом AccessDecision.
func authorize(ctx context.Context, access ports.AccessDecision, actor domain.ActorID, tenant domain.TenantID) error {
	allowed, err := access.CanAct(ctx, actor, tenant)
	if err != nil {
		return fmt.Errorf("проверка права актора %s в тенанте %s: %w", actor, tenant, err)
	}
	if !allowed {
		return fmt.Errorf("%w: актор %s, тенант %s", ErrAccessDenied, actor, tenant)
	}
	return nil
}

// loadCampaign загружает агрегат в рамках транзакции и проверяет границу
// тенанта. Порт различает «кампании нет» (ports.ErrCampaignNotFound) и отказ
// хранилища; обе причины пробрасываются вызывающему без потери.
func loadCampaign(
	ctx context.Context,
	repo ports.CampaignRepository,
	id domain.CampaignID,
	tenant domain.TenantID,
) (*domain.Campaign, error) {
	campaign, err := repo.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("загрузка кампании %s: %w", id, err)
	}
	if campaign == nil {
		return nil, fmt.Errorf("загрузка кампании %s: %w", id, ports.ErrCampaignNotFound)
	}
	if campaign.TenantID() != tenant {
		return nil, fmt.Errorf("%w: кампания %s", ErrCampaignTenantMismatch, id)
	}
	return campaign, nil
}
