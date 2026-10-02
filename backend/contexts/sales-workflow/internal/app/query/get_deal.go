package query

import (
	"context"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// GetDeal — запрос состояния сделки.
type GetDeal struct {
	DealID   domain.DealID
	TenantID domain.TenantID
}

// DealView — представление состояния сделки для вызывающего.
type DealView struct {
	DealID           domain.DealID          `json:"deal_id"`
	TenantID         domain.TenantID        `json:"tenant_id"`
	CompanyRef       domain.CompanyRef      `json:"company_ref"`
	Stage            domain.DealStage       `json:"stage"`
	ExternalRef      domain.ExternalDealRef `json:"external_ref"`
	ExternalRevision int64                  `json:"external_revision"`
	ResultConfirmed  bool                   `json:"result_confirmed"`
	ActionRef        domain.ActionRef       `json:"action_ref,omitempty"`
	Version          int                    `json:"version"`
}

// GetDealHandler — use case чтения сделки.
type GetDealHandler struct {
	deals ports.DealRepository
}

// NewGetDealHandler собирает use case.
func NewGetDealHandler(deals ports.DealRepository) *GetDealHandler {
	return &GetDealHandler{deals: deals}
}

// Handle загружает сделку из внешней CRM и собирает представление.
func (h *GetDealHandler) Handle(ctx context.Context, q GetDeal) (DealView, error) {
	deal, err := h.deals.FindByID(ctx, q.DealID)
	if err != nil {
		return DealView{}, err
	}
	if deal.TenantID() != q.TenantID {
		return DealView{}, ErrTenantMismatch
	}
	view := DealView{
		DealID:           deal.ID(),
		TenantID:         deal.TenantID(),
		CompanyRef:       deal.CompanyRef(),
		Stage:            deal.Stage(),
		ExternalRef:      deal.External().Ref(),
		ExternalRevision: deal.External().Revision(),
		ResultConfirmed:  deal.ResultConfirmed(),
		Version:          deal.Version(),
	}
	if proof, ok := deal.Attribution(); ok {
		view.ActionRef = proof.ActionRef()
	}
	return view, nil
}
