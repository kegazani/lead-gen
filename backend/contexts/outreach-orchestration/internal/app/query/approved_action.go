package query

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// Исходы чтения решения по черновику. Различают «черновика/решения нет», «решение
// ещё не принято» и «оператор отклонил» — эти состояния не смешиваются, и ни одно
// не выдаётся за положительное решение.
var (
	// ErrDraftDecisionNotFound — черновик по ссылке не найден, запрошенная версия не
	// совпадает с текущей (черновик доработан или версия неизвестна) либо
	// действующего решения по запрошенной версии нет.
	ErrDraftDecisionNotFound = errors.New("запросы: решение по черновику не найдено")
	// ErrDraftDecisionPending — черновик найден, но решение по текущей версии ещё
	// не принято.
	ErrDraftDecisionPending = errors.New("запросы: решение по черновику ещё не принято")
	// ErrDraftDecisionRejected — оператор отклонил черновик: отправка не санкционирована.
	ErrDraftDecisionRejected = errors.New("запросы: оператор отклонил черновик")
)

// ApprovedActionQuery — запрос утверждённого действия по черновику.
//
// Адресация — только непрозрачная ссылка на черновик и его точная версия: ни
// кампании, ни запуска в запросе нет. Ссылка черновика уникальна в тенанте, поэтому
// кампания выводится из (TenantID, DraftRef) детерминированно (domain.DraftCampaignID) —
// так читающий контракт разрешает агрегат без отдельного индекса.
type ApprovedActionQuery struct {
	TenantID     domain.TenantID
	DraftRef     domain.DraftID
	DraftVersion int
}

// ApprovedActionHandler — use case чтения утверждённого действия по черновику для
// шага отправки (контракт draftdecision, владелец — этот контекст).
//
// Доступ ограничен тенантом: запрос межсервисной границы не несёт актора-оператора,
// поэтому границу держит тенант, а кампания чужого тенанта не раскрывается даже
// фактом существования. Возвращаемое действие соответствует решению оператора
// только по точной запрошенной версии черновика: запрос версии, не совпадающей с
// текущей, не выдаёт решение иной версии (изменённый черновик требует нового решения).
type ApprovedActionHandler struct {
	campaigns ports.CampaignRepository
}

// NewApprovedActionHandler собирает запрос из порта репозитория кампаний.
func NewApprovedActionHandler(campaigns ports.CampaignRepository) *ApprovedActionHandler {
	return &ApprovedActionHandler{campaigns: campaigns}
}

// Handle возвращает утверждённое действие по черновику. Решение действует только
// для точной запрошенной версии, совпадающей с текущей версией черновика:
// положительное решение — действие этой версии, отклонение — ErrDraftDecisionRejected,
// отсутствие решения по текущей версии — ErrDraftDecisionPending, неизвестный
// черновик, несовпадение запрошенной версии с текущей или отсутствие действующего
// решения — ErrDraftDecisionNotFound.
func (h *ApprovedActionHandler) Handle(ctx context.Context, q ApprovedActionQuery) (approvedaction.ApprovedAction, error) {
	tenant := domain.TenantID(strings.TrimSpace(q.TenantID.String()))
	draftRef := domain.DraftID(strings.TrimSpace(q.DraftRef.String()))
	if tenant == "" {
		return approvedaction.ApprovedAction{}, ErrTenantRequired
	}
	if draftRef == "" || q.DraftVersion <= 0 {
		return approvedaction.ApprovedAction{}, ErrDraftDecisionNotFound
	}

	campaign, err := h.campaigns.FindByID(ctx, domain.DraftCampaignID(tenant, draftRef))
	if err != nil {
		if errors.Is(err, ports.ErrCampaignNotFound) {
			return approvedaction.ApprovedAction{}, ErrDraftDecisionNotFound
		}
		return approvedaction.ApprovedAction{}, fmt.Errorf("загрузка кампании черновика %s: %w", draftRef, err)
	}
	// Кампания чужого тенанта не раскрывается даже фактом существования.
	if campaign == nil || campaign.TenantID() != tenant {
		return approvedaction.ApprovedAction{}, ErrDraftDecisionNotFound
	}
	if campaign.Purpose() == domain.CampaignPurposeEditorial {
		return approvedaction.ApprovedAction{}, ErrDraftDecisionNotFound
	}

	draft, ok := campaign.Draft(draftRef)
	if !ok {
		return approvedaction.ApprovedAction{}, ErrDraftDecisionNotFound
	}

	// Решение действует только для точной запрошенной версии, совпадающей с текущей
	// версией черновика. Доработка повышает версию и снимает прежнее решение
	// (инвариант I2), поэтому запрос устаревшей или неизвестной версии не выдаёт
	// решение иной версии за действующее.
	if q.DraftVersion != draft.Version() {
		return approvedaction.ApprovedAction{}, ErrDraftDecisionNotFound
	}

	decision, decided := decisionForVersion(campaign, draft.ID(), q.DraftVersion)
	if decided {
		if !decision.IsApproval() {
			return approvedaction.ApprovedAction{}, ErrDraftDecisionRejected
		}
		return buildApprovedAction(campaign, draft, decision), nil
	}
	switch draft.State() {
	case domain.DraftStatePendingApproval, domain.DraftStateProposed:
		return approvedaction.ApprovedAction{}, ErrDraftDecisionPending
	default:
		return approvedaction.ApprovedAction{}, ErrDraftDecisionNotFound
	}
}

// decisionForVersion возвращает решение по точной версии черновика, если оно есть.
// Журнал решений — выборка сущностей operator-decision по черновику (отдельной
// модели нет), поэтому решение ищется по точной версии: прежнее решение к
// изменившейся версии не относится.
func decisionForVersion(campaign *domain.Campaign, draftID domain.DraftID, version int) (domain.OperatorDecision, bool) {
	for _, decision := range campaign.DecisionsForDraft(draftID) {
		if decision.DraftVersion() == version {
			return decision, true
		}
	}
	return domain.OperatorDecision{}, false
}

// buildApprovedAction собирает утверждённое действие из решения и точной версии
// черновика. Отображение берётся из единственного определения
// (contracts.FromActionApproved) — того же, которым relay публикует действие, —
// поэтому поля и ключ идемпотентности совпадают с публикацией. EventID задаётся
// стабильным идентификатором решения: событие снимается из агрегата, а не из outbox,
// но действие обязано быть валидным для повторной публикации шагом отправки.
func buildApprovedAction(campaign *domain.Campaign, draft *domain.ActionDraft, decision domain.OperatorDecision) approvedaction.ApprovedAction {
	action := contracts.FromActionApproved(domain.ActionApproved{
		CampaignID:   campaign.ID(),
		TenantID:     campaign.TenantID(),
		DraftID:      draft.ID(),
		DraftVersion: decision.DraftVersion(),
		DecisionID:   decision.ID(),
		Actor:        decision.Actor(),
		Channel:      draft.Channel(),
		Material:     draft.Spec().Material,
		Recipient:    draft.Spec().Recipient,
		At:           decision.DecidedAt(),
	})
	if action.EventID == "" {
		action.EventID = action.ApprovedActionID
	}
	return action
}
