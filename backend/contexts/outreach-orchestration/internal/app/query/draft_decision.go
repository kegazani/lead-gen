package query

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// DraftDecisionJournal — запрос журнала решений по черновику.
//
// TenantID и Actor обязательны: тенант ограничивает область данных, актор
// подтверждает право на чтение. Actor служит только проверкой доступа: журнал
// возвращает все решения по черновику в пределах тенанта, включая решения
// других операторов, — иначе аудит терял бы часть записей.
type DraftDecisionJournal struct {
	CampaignID domain.CampaignID
	TenantID   domain.TenantID
	DraftID    domain.DraftID
	Actor      domain.ActorID
}

// DraftDecisionEntry — запись журнала: автор, время и результат решения.
//
// Batch — признак пакетного принятия. Ссылки на пакет здесь нет: собственного
// идентификатора у пакета не существует, а журнал возвращает решение самого
// черновика. Признак лишь отличает пакетное решение от одиночного при аудите
// (design.md, Решение 4).
type DraftDecisionEntry struct {
	DecisionID   domain.DecisionID
	CampaignID   domain.CampaignID
	DraftID      domain.DraftID
	DraftVersion int
	Actor        domain.ActorID
	Outcome      domain.DecisionOutcome
	Reason       string
	DecidedAt    time.Time
	Batch        bool
}

// DraftDecisionHandler — use case чтения журнала решений.
type DraftDecisionHandler struct {
	campaigns ports.CampaignRepository
	access    ports.AccessDecision
}

// NewDraftDecisionHandler собирает запрос из порта репозитория кампаний.
//
// Порт доступа задаётся вторым аргументом. Он необязателен только ради
// обратной совместимости сборки: без него любой запрос чтения отвергается с
// ErrAccessDenied, а не обслуживается без проверки.
func NewDraftDecisionHandler(campaigns ports.CampaignRepository, access ...ports.AccessDecision) *DraftDecisionHandler {
	return &DraftDecisionHandler{campaigns: campaigns, access: optionalAccess(access)}
}

// Handle возвращает решения по черновику. Отдельной модели журнала нет:
// выборка идёт из решений агрегата, поэтому автор, время и результат решения
// берутся из одной сущности operator-decision.
func (h *DraftDecisionHandler) Handle(ctx context.Context, q DraftDecisionJournal) ([]DraftDecisionEntry, error) {
	if err := authorizeRead(ctx, h.access, q.Actor, q.TenantID); err != nil {
		return nil, err
	}

	campaign, err := h.campaigns.FindByID(ctx, q.CampaignID)
	if err != nil {
		return nil, fmt.Errorf("загрузка кампании %s: %w", q.CampaignID, err)
	}
	if campaign == nil {
		return nil, fmt.Errorf("загрузка кампании %s: %w", q.CampaignID, ports.ErrCampaignNotFound)
	}
	// Тенант обязателен: кампания другого тенанта не раскрывается даже фактом
	// существования.
	if campaign.TenantID() != q.TenantID {
		return nil, fmt.Errorf("загрузка кампании %s: %w", q.CampaignID, ports.ErrCampaignNotFound)
	}

	var entries []DraftDecisionEntry
	for _, decision := range campaign.DecisionsForDraft(q.DraftID) {
		entries = append(entries, DraftDecisionEntry{
			DecisionID:   decision.ID(),
			CampaignID:   decision.CampaignID(),
			DraftID:      decision.DraftID(),
			DraftVersion: decision.DraftVersion(),
			Actor:        decision.Actor(),
			Outcome:      decision.Outcome(),
			Reason:       decision.Reason(),
			DecidedAt:    decision.DecidedAt(),
			Batch:        decision.IsBatchDecision(),
		})
	}
	return entries, nil
}
