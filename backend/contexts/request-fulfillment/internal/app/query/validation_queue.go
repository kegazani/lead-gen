package query

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ValidationQueue — запрос очереди валидации: заявки тенанта в статусе submitted.
type ValidationQueue struct {
	TenantID domain.TenantID
}

// ValidationItem — строка очереди валидации: идентификаторы и оффер.
type ValidationItem struct {
	RequestID string
	Title     string
	Status    domain.RequestStatus
	Offer     OfferView
	Version   int
}

// ValidationQueueHandler — use case чтения очереди валидации.
type ValidationQueueHandler struct {
	requests ports.RequestRepository
}

// NewValidationQueueHandler собирает use case очереди валидации.
func NewValidationQueueHandler(requests ports.RequestRepository) *ValidationQueueHandler {
	return &ValidationQueueHandler{requests: requests}
}

// Handle возвращает заявки на валидацию. Чужие тенанты не попадают в выборку:
// изоляция обеспечивается фильтром владельца, а не экраном.
func (h *ValidationQueueHandler) Handle(ctx context.Context, q ValidationQueue) ([]ValidationItem, error) {
	requests, err := h.requests.ListByStatus(ctx, domain.StatusSubmitted)
	if err != nil {
		return nil, err
	}
	items := make([]ValidationItem, 0, len(requests))
	for _, request := range requests {
		if request.TenantID() != q.TenantID {
			continue
		}
		items = append(items, ValidationItem{
			RequestID: request.ID().String(),
			Title:     request.Title(),
			Status:    request.Status(),
			Offer:     offerView(request.Offer()),
			Version:   request.Version(),
		})
	}
	return items, nil
}
