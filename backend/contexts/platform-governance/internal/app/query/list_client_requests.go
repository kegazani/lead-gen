package query

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// ListClientRequests — запрос списка заявок клиента. TenantID обязателен: чтение
// заявок другого тенанта не выполняется (контракт request-intent).
type ListClientRequests struct {
	TenantID string
}

// ClientRequestView — строка истории заявок клиента: идентификатор и текущий
// статус. Опубликованный контракт List несёт только текущий статус каждой заявки
// (requestintent.ListEntry{RequestID, Status}), поэтому проекция не выдумывает
// историю переходов статусов: дерево показывает фактическое текущее состояние.
type ClientRequestView struct {
	RequestID string
	Status    string
}

// ListClientRequestsHandler — use case чтения истории заявок клиента.
type ListClientRequestsHandler struct {
	requests requestintent.Port
}

// NewListClientRequestsHandler собирает use case из контракта владельца заявки.
func NewListClientRequestsHandler(requests requestintent.Port) *ListClientRequestsHandler {
	return &ListClientRequestsHandler{requests: requests}
}

// Handle возвращает список заявок клиента в порядке, который отдал владелец.
// Ошибка владельца (в том числе недоступность контракта) переводится в исход
// сценария и не маскируется пустым списком.
func (h *ListClientRequestsHandler) Handle(ctx context.Context, q ListClientRequests) ([]ClientRequestView, error) {
	entries, err := h.requests.List(ctx, requestintent.ListQuery{
		TenantID: strings.TrimSpace(q.TenantID),
	})
	if err != nil {
		return nil, app.TranslateRequestIntent(err)
	}
	views := make([]ClientRequestView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, ClientRequestView{RequestID: entry.RequestID, Status: entry.Status})
	}
	return views, nil
}
