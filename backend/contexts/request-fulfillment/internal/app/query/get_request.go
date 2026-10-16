package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// GetRequest — запрос заявки по идентификатору.
type GetRequest struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
}

// OfferView — представление версии оффера.
type OfferView struct {
	OfferID  string
	Content  string
	Target   string
	Revision int
}

// TransitionView — представление записи журнала переходов.
type TransitionView struct {
	From   string
	To     string
	Actor  string
	At     string
	Reason string
}

// ValidationDecisionView — представление решения валидации.
type ValidationDecisionView struct {
	Outcome string
	Actor   string
	At      string
	Reason  string
}

// RequestView — представление заявки для чтения.
type RequestView struct {
	ExecutionMode      string
	ResearchSelection  *domain.ResearchSelection
	RequestID          string
	TenantID           string
	Status             domain.RequestStatus
	Version            int
	Offer              OfferView
	OfferHistory       []OfferView
	AudienceSnapshotID string
	CampaignID         string
	DealID             string
	CallID             string
	// Phase — фаза исполнения (для упавшей заявки — "failed", иначе пусто).
	Phase string
	// ErrorReason — нормализованный код причины сбоя исполнения (пуст вне failed).
	ErrorReason string
	// ErrorMessage — текст причины сбоя исполнения (пуст вне failed).
	ErrorMessage       string
	ExecutionPurpose   string
	ValidationDecision *ValidationDecisionView
	Transitions        []TransitionView
}

// GetRequestHandler — use case чтения заявки.
type GetRequestHandler struct {
	requests ports.RequestRepository
}

// NewGetRequestHandler собирает use case чтения.
func NewGetRequestHandler(requests ports.RequestRepository) *GetRequestHandler {
	return &GetRequestHandler{requests: requests}
}

// Handle загружает заявку и строит представление. Заявка другого тенанта
// неотличима от отсутствующей: её существование не раскрывается.
func (h *GetRequestHandler) Handle(ctx context.Context, q GetRequest) (RequestView, error) {
	request, err := h.requests.FindByID(ctx, q.RequestID)
	if err != nil {
		return RequestView{}, err
	}
	if request.TenantID() != q.TenantID {
		return RequestView{}, fmt.Errorf("%w: %s", ports.ErrRequestNotFound, q.RequestID)
	}
	return buildView(request), nil
}

// buildView переводит агрегат в представление чтения.
func buildView(request *domain.Request) RequestView {
	view := RequestView{
		ExecutionMode: request.ExecutionMode(), ResearchSelection: request.ResearchSelection(),
		RequestID:          request.ID().String(),
		TenantID:           request.TenantID().String(),
		Status:             request.Status(),
		Version:            request.Version(),
		Offer:              offerView(request.Offer()),
		AudienceSnapshotID: request.AudienceSnapshotID().String(),
		CampaignID:         request.CampaignID().String(),
		DealID:             request.DealID().String(),
		CallID:             request.CallID().String(),
		ExecutionPurpose:   request.ExecutionPurpose(),
	}
	if decision, ok := request.ValidationDecision(); ok {
		view.ValidationDecision = &ValidationDecisionView{
			Outcome: decision.Outcome().String(),
			Actor:   decision.Actor().String(),
			At:      decision.At().UTC().Format("2006-01-02T15:04:05Z07:00"),
			Reason:  decision.Reason(),
		}
	}
	if code, text, ok := request.FailureReason(); ok {
		view.Phase = string(domain.StatusFailed)
		view.ErrorReason = code
		view.ErrorMessage = text
	}
	for _, offer := range request.OfferHistory() {
		view.OfferHistory = append(view.OfferHistory, offerView(&offer))
	}
	for _, transition := range request.Transitions() {
		view.Transitions = append(view.Transitions, TransitionView{
			From:   string(transition.From()),
			To:     string(transition.To()),
			Actor:  transition.Actor().String(),
			At:     transition.At().UTC().Format("2006-01-02T15:04:05Z07:00"),
			Reason: transition.Reason(),
		})
	}
	return view
}

// offerView переводит оффер в представление чтения.
func offerView(offer *domain.Offer) OfferView {
	if offer == nil {
		return OfferView{}
	}
	return OfferView{
		OfferID:  offer.ID().String(),
		Content:  offer.Content().Text(),
		Target:   offer.Target().Description(),
		Revision: offer.Revision(),
	}
}
