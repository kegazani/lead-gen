package query

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ListRequests — запрос списка заявок тенанта для истории клиента.
type ListRequests struct {
	TenantID domain.TenantID
}

// ListRequestItem — строка списка заявок: идентификатор и текущий статус.
type ListRequestItem struct {
	RequestID string
	Status    domain.RequestStatus
}

// ListRequestsHandler — use case чтения списка заявок тенанта.
type ListRequestsHandler struct {
	requests ports.RequestRepository
}

// NewListRequestsHandler собирает use case списка заявок.
func NewListRequestsHandler(requests ports.RequestRepository) *ListRequestsHandler {
	return &ListRequestsHandler{requests: requests}
}

// Handle возвращает заявки тенанта в порядке по идентификатору. Чужие тенанты в
// выборку не попадают: изоляция обеспечивается фильтром репозитория, а не
// вызывающим.
func (h *ListRequestsHandler) Handle(ctx context.Context, q ListRequests) ([]ListRequestItem, error) {
	requests, err := h.requests.ListByTenant(ctx, q.TenantID)
	if err != nil {
		return nil, err
	}
	items := make([]ListRequestItem, 0, len(requests))
	for _, request := range requests {
		items = append(items, ListRequestItem{
			RequestID: request.ID().String(),
			Status:    request.Status(),
		})
	}
	return items, nil
}
