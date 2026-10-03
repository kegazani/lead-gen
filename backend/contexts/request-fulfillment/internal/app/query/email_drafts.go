package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// EmailDrafts — запрос черновиков писем заявки.
type EmailDrafts struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
}

// EmailDraftView — представление черновика письма для чтения оператором.
type EmailDraftView struct {
	LeadID   string
	Subject  string
	Body     string
	Language string
}

// EmailDraftsHandler — use case чтения черновиков писем заявки.
type EmailDraftsHandler struct {
	requests ports.RequestRepository
	drafts   ports.EmailDraftStore
}

// NewEmailDraftsHandler собирает use case чтения черновиков. Хранилище допускается
// равным nil: тогда чтение честно отвечает исходом «хранилище недоступно», а не
// выдуманным пустым списком.
func NewEmailDraftsHandler(requests ports.RequestRepository, drafts ports.EmailDraftStore) *EmailDraftsHandler {
	return &EmailDraftsHandler{requests: requests, drafts: drafts}
}

// Handle читает черновики заявки. Заявка другого тенанта неотличима от
// отсутствующей: её существование не раскрывается.
func (h *EmailDraftsHandler) Handle(ctx context.Context, q EmailDrafts) ([]EmailDraftView, error) {
	if h.drafts == nil {
		return nil, ports.ErrDraftStoreUnavailable
	}
	request, err := h.requests.FindByID(ctx, q.RequestID)
	if err != nil {
		return nil, err
	}
	if request.TenantID() != q.TenantID {
		return nil, fmt.Errorf("%w: %s", ports.ErrRequestNotFound, q.RequestID)
	}
	stored, err := h.drafts.ListByRequest(ctx, q.RequestID.String(), q.TenantID.String())
	if err != nil {
		return nil, err
	}
	views := make([]EmailDraftView, 0, len(stored))
	for _, draft := range stored {
		views = append(views, EmailDraftView{
			LeadID:   draft.LeadID,
			Subject:  draft.Subject,
			Body:     draft.Body,
			Language: draft.Language,
		})
	}
	return views, nil
}
