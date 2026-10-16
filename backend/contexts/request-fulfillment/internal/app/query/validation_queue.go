package query

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ValidationQueue — запрос операторского списка заявок тенанта.
type ValidationQueue struct {
	TenantID domain.TenantID
}

// ValidationItem — строка очереди валидации: идентификаторы и оффер.
type ValidationItem struct {
	ExecutionMode string
	RequestID     string
	Status        domain.RequestStatus
	Offer         OfferView
	Version       int
}

// ValidationQueueHandler — use case чтения очереди валидации.
type ValidationQueueHandler struct {
	requests ports.RequestRepository
}

// NewValidationQueueHandler собирает use case очереди валидации.
func NewValidationQueueHandler(requests ports.RequestRepository) *ValidationQueueHandler {
	return &ValidationQueueHandler{requests: requests}
}

// Handle возвращает заявки на всех стадиях, включая поиск и завершение.
// Чужие тенанты не попадают в выборку:
// изоляция обеспечивается фильтром владельца, а не экраном.
func (h *ValidationQueueHandler) Handle(ctx context.Context, q ValidationQueue) ([]ValidationItem, error) {
	requests, err := h.requests.ListByTenant(ctx, q.TenantID)
	if err != nil {
		return nil, err
	}
	items := make([]ValidationItem, 0, len(requests))
	for _, request := range requests {
		if request.TenantID() != q.TenantID {
			continue
		}
		items = append(items, ValidationItem{
			ExecutionMode: request.ExecutionMode(),
			RequestID:     request.ID().String(),
			Status:        request.Status(),
			Offer:         offerView(request.Offer()),
			Version:       request.Version(),
		})
	}
	return items, nil
}
