// Package query содержит прикладные запросы чтения контекста
// outreach-orchestration: очередь ожидающих решения черновиков и журнал
// решений по черновику.
//
// Запросы не меняют состояние: они загружают агрегат через порт
// CampaignRepository и возвращают выборку по публичному API домена. Журнал
// решений — это выборка сущностей operator-decision, а не отдельная модель
// (design.md, Решение 3).
//
// Читающие пути проходят ту же проверку доступа, что и команды: актор и тенант
// обязательны, а порт AccessDecision должен дать положительное решение. Пустой
// TenantID — ошибка запроса, а не «все тенанты» (design.md, Решение 8).
package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// ApprovalQueue — запрос очереди черновиков, ожидающих решения оператора.
//
// Порт CampaignRepository загружает агрегат по идентификатору и не умеет
// перечислять кампании, поэтому область запроса задаёт вызывающий: CampaignIDs
// — это кампании, входящие в очередь. TenantID и Actor обязательны: тенант
// ограничивает область данных, актор подтверждает право на чтение.
type ApprovalQueue struct {
	CampaignIDs []domain.CampaignID
	TenantID    domain.TenantID
	Actor       domain.ActorID
}

// ApprovalQueueItem — позиция очереди: черновик, ожидающий решения.
type ApprovalQueueItem struct {
	CampaignID   domain.CampaignID
	TenantID     domain.TenantID
	DraftID      domain.DraftID
	DraftVersion int
	Channel      domain.ActionChannel
	Spec         domain.DraftSpec
}

// ApprovalQueueHandler — use case чтения очереди утверждения.
type ApprovalQueueHandler struct {
	campaigns ports.CampaignRepository
	access    ports.AccessDecision
}

// NewApprovalQueueHandler собирает запрос из порта репозитория кампаний.
//
// Порт доступа задаётся вторым аргументом. Он необязателен только ради
// обратной совместимости сборки: без него любой запрос чтения отвергается с
// ErrAccessDenied, а не обслуживается без проверки.
func NewApprovalQueueHandler(campaigns ports.CampaignRepository, access ...ports.AccessDecision) *ApprovalQueueHandler {
	return &ApprovalQueueHandler{campaigns: campaigns, access: optionalAccess(access)}
}

// Handle возвращает черновики, ожидающие решения. Очередь строится публичным
// API агрегата: PendingApproval уже исключает утверждённые и отклонённые
// черновики, поэтому запрос не повторяет доменное правило состояния.
func (h *ApprovalQueueHandler) Handle(ctx context.Context, q ApprovalQueue) ([]ApprovalQueueItem, error) {
	if err := authorizeRead(ctx, h.access, q.Actor, q.TenantID); err != nil {
		return nil, err
	}

	var items []ApprovalQueueItem
	for _, id := range q.CampaignIDs {
		campaign, err := h.campaigns.FindByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, err)
		}
		if campaign == nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, ports.ErrCampaignNotFound)
		}
		// Тенант обязателен, поэтому кампания чужого тенанта в выборку не попадает.
		if campaign.TenantID() != q.TenantID {
			continue
		}
		for _, draft := range campaign.PendingApproval() {
			items = append(items, ApprovalQueueItem{
				CampaignID:   campaign.ID(),
				TenantID:     campaign.TenantID(),
				DraftID:      draft.ID(),
				DraftVersion: draft.Version(),
				Channel:      draft.Channel(),
				Spec:         draft.Spec(),
			})
		}
	}
	return items, nil
}
