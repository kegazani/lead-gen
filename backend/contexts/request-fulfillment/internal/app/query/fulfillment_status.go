package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// FulfillmentStatus — запрос статусов артефактов фулфилмента по ссылкам заявки.
type FulfillmentStatus struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
}

// ArtifactStatusView — прочитанный статус артефакта фулфилмента.
type ArtifactStatusView struct {
	Kind   ports.ArtifactKind
	ID     string
	Status string
	Found  bool
}

// FulfillmentStatusView — результат чтения статусов артефактов заявки.
type FulfillmentStatusView struct {
	RequestID string
	Status    domain.RequestStatus
	Artifacts []ArtifactStatusView
}

// FulfillmentStatusHandler — use case чтения статусов артефактов фулфилмента.
type FulfillmentStatusHandler struct {
	requests ports.RequestRepository
	query    ports.FulfillmentQuery
}

// NewFulfillmentStatusHandler собирает use case из репозитория заявок и
// синхронного query-контракта владельцев артефактов.
func NewFulfillmentStatusHandler(requests ports.RequestRepository, query ports.FulfillmentQuery) *FulfillmentStatusHandler {
	return &FulfillmentStatusHandler{requests: requests, query: query}
}

// Handle загружает заявку и читает статусы её артефактов по идентификаторам.
// Заявка не владеет чужими агрегатами: статусы читаются через контракт владельца,
// а отсутствие ссылки означает, что артефакт ещё не зафиксирован.
func (h *FulfillmentStatusHandler) Handle(ctx context.Context, q FulfillmentStatus) (FulfillmentStatusView, error) {
	request, err := h.requests.FindByID(ctx, q.RequestID)
	if err != nil {
		return FulfillmentStatusView{}, err
	}
	if request.TenantID() != q.TenantID {
		return FulfillmentStatusView{}, fmt.Errorf("%w: %s", ports.ErrRequestNotFound, q.RequestID)
	}

	view := FulfillmentStatusView{
		RequestID: request.ID().String(),
		Status:    request.Status(),
	}
	for _, ref := range refs(request) {
		status, err := h.query.Status(ctx, ref)
		if err != nil {
			return FulfillmentStatusView{}, fmt.Errorf("чтение статуса артефакта %s:%s: %w", ref.Kind, ref.ID, err)
		}
		view.Artifacts = append(view.Artifacts, ArtifactStatusView{
			Kind:   status.Ref.Kind,
			ID:     status.Ref.ID,
			Status: status.Status,
			Found:  status.Found,
		})
	}
	return view, nil
}

// refs собирает зафиксированные ссылки заявки на артефакты фулфилмента.
func refs(request *domain.Request) []ports.ArtifactRef {
	var out []ports.ArtifactRef
	if id := request.AudienceSnapshotID(); id != "" {
		out = append(out, ports.ArtifactRef{Kind: ports.ArtifactAudienceSnapshot, ID: id.String()})
	}
	if id := request.CampaignID(); id != "" {
		out = append(out, ports.ArtifactRef{Kind: ports.ArtifactCampaign, ID: id.String()})
	}
	if id := request.DealID(); id != "" {
		out = append(out, ports.ArtifactRef{Kind: ports.ArtifactDeal, ID: id.String()})
	}
	return out
}
