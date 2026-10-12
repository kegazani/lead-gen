package query

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/projection"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// PortalState — запрос состояния портала клиента. RequestID пуст — берётся
// последняя поданная заявка тенанта из проекции портала.
type PortalState struct {
	TenantID  string
	RequestID string
}

// PortalView — проекция состояния для клиента: только идентификатор и статус
// заявки.
//
// Это явная проекция маскирования (задача 4.1): сырых лидов, карточек компаний,
// контактов и процесса краулинга в ней нет по построению. Поля оффера владельца
// (OfferContent/OfferTarget) намеренно не переносятся — клиентский ответ их не
// содержит.
type PortalView struct {
	TenantID     string
	RequestID    string
	Status       string
	ErrorMessage string
	ErrorReason  string
	HasRequest   bool
}

// PortalStateHandler — use case чтения состояния портала.
type PortalStateHandler struct {
	requests   requestintent.Port
	projection *projection.LastRequest
}

// NewPortalStateHandler собирает use case из контракта владельца заявки и
// проекции портала.
func NewPortalStateHandler(requests requestintent.Port, proj *projection.LastRequest) *PortalStateHandler {
	return &PortalStateHandler{requests: requests, projection: proj}
}

// Handle возвращает состояние заявки клиента. Чужой тенант и отсутствие заявки
// неотличимы: оба дают app.ErrRequestNotFound, а не раскрывают существование
// чужой заявки. Наличие заявки определяется по её идентификатору.
func (h *PortalStateHandler) Handle(ctx context.Context, q PortalState) (PortalView, error) {
	requestID := strings.TrimSpace(q.RequestID)
	if requestID == "" {
		if last, ok := h.projection.Get(domain.TenantID(q.TenantID)); ok {
			requestID = last
		}
	}
	if requestID == "" {
		return PortalView{TenantID: q.TenantID}, nil
	}
	result, err := h.requests.Status(ctx, requestintent.StatusQuery{
		RequestID: requestID,
		TenantID:  q.TenantID,
	})
	if err != nil {
		return PortalView{}, app.TranslateRequestIntent(err)
	}
	return PortalView{
		TenantID:     q.TenantID,
		RequestID:    result.RequestID,
		Status:       result.Status,
		ErrorMessage: result.ErrorMessage,
		ErrorReason:  result.ErrorReason,
		HasRequest:   true,
	}, nil
}
