package query

import (
	"context"
	"fmt"
	"sort"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// StalledDrafts — запрос среза зависших черновиков: черновиков, у которых истёк
// срок утверждения без автоутверждения. Действие по ним не исполнено, а оператор
// об этом уведомляется.
//
// TenantID и Actor обязательны: тенант ограничивает область данных, актор
// подтверждает право на чтение.
type StalledDrafts struct {
	// CampaignID сужает срез до одной кампании. Пусто — все кампании тенанта.
	CampaignID domain.CampaignID

	TenantID domain.TenantID
	Actor    domain.ActorID
}

// StalledDraftItem — позиция среза зависших черновиков.
//
// Execution == false всегда: просроченный без автоутверждения черновик остаётся
// неисполненным. Поле названо явно, чтобы «завис, но исполнен» нельзя было
// показать как норму.
type StalledDraftItem struct {
	CampaignID   domain.CampaignID
	DraftID      domain.DraftID
	DraftVersion int
	Channel      domain.ActionChannel
	Spec         domain.DraftSpec
	State        domain.DraftState
	Executed     bool
}

// StalledDraftsResult — результат среза. Notify истинно, когда есть зависшие
// черновики, требующие внимания оператора.
type StalledDraftsResult struct {
	Items  []StalledDraftItem
	Notify bool
}

// StalledDraftsHandler — use case чтения среза зависших черновиков.
type StalledDraftsHandler struct {
	campaigns ports.CampaignRepository
	lister    ports.CampaignLister
	access    ports.AccessDecision
}

// NewStalledDraftsHandler собирает запрос из порта репозитория, перечислителя
// кампаний и (необязательного) порта доступа. Без порта доступа любой запрос
// отвергается с ErrAccessDenied, а не обслуживается без проверки.
func NewStalledDraftsHandler(
	campaigns ports.CampaignRepository,
	lister ports.CampaignLister,
	access ...ports.AccessDecision,
) *StalledDraftsHandler {
	return &StalledDraftsHandler{campaigns: campaigns, lister: lister, access: optionalAccess(access)}
}

// Handle возвращает черновики в состоянии «срок истёк». Автоутверждение запрещено
// доменом, поэтому такие черновики не исполнены: срез лишь показывает их
// оператору и выставляет признак уведомления.
func (h *StalledDraftsHandler) Handle(ctx context.Context, q StalledDrafts) (StalledDraftsResult, error) {
	if err := authorizeRead(ctx, h.access, q.Actor, q.TenantID); err != nil {
		return StalledDraftsResult{}, err
	}

	ids, err := h.scope(ctx, q)
	if err != nil {
		return StalledDraftsResult{}, err
	}

	var items []StalledDraftItem
	for _, id := range ids {
		campaign, err := h.campaigns.FindByID(ctx, id)
		if err != nil {
			return StalledDraftsResult{}, fmt.Errorf("загрузка кампании %s: %w", id, err)
		}
		if campaign == nil {
			return StalledDraftsResult{}, fmt.Errorf("загрузка кампании %s: %w", id, ports.ErrCampaignNotFound)
		}
		if campaign.TenantID() != q.TenantID {
			continue
		}
		for _, draft := range campaign.Drafts() {
			if draft.State() != domain.DraftStateExpired {
				continue
			}
			items = append(items, StalledDraftItem{
				CampaignID:   campaign.ID(),
				DraftID:      draft.ID(),
				DraftVersion: draft.Version(),
				Channel:      draft.Channel(),
				Spec:         draft.Spec(),
				State:        draft.State(),
				// Просроченный черновик не исполнен: состояние «executed»
				// недостижимо из «срок истёк» (инвариант I1), автоутверждения
				// при истечении срока не происходит.
				Executed: false,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].CampaignID != items[j].CampaignID {
			return items[i].CampaignID < items[j].CampaignID
		}
		return items[i].DraftID < items[j].DraftID
	})
	return StalledDraftsResult{Items: items, Notify: len(items) > 0}, nil
}

// scope возвращает идентификаторы кампаний для выборки: одна кампания при
// заданном фильтре, иначе — все сохранённые.
func (h *StalledDraftsHandler) scope(ctx context.Context, q StalledDrafts) ([]domain.CampaignID, error) {
	if q.CampaignID != "" {
		return []domain.CampaignID{q.CampaignID}, nil
	}
	ids, err := h.lister.ListCampaignIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("перечисление кампаний: %w", err)
	}
	return ids, nil
}
