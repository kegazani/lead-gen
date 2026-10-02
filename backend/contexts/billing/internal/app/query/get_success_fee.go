package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// SuccessFeeView — представление вознаграждения за подтверждённый результат.
type SuccessFeeView struct {
	SuccessFeeID   string `json:"success_fee_id"`
	TenantID       string `json:"tenant_id"`
	SubscriptionID string `json:"subscription_id"`
	ResultRef      string `json:"result_ref"`
	DealID         string `json:"deal_id"`
	BaseMinor      int64  `json:"base_minor"`
	Currency       string `json:"currency"`
	RateBps        int64  `json:"rate_bps"`
	AmountMinor    int64  `json:"amount_minor"`
	Calculation    string `json:"calculation_version"`
	Status         string `json:"status"`
	Version        int    `json:"version"`
}

// GetSuccessFee — запрос вознаграждения по идентификатору.
type GetSuccessFee struct {
	SuccessFeeID domain.SuccessFeeID
	TenantID     domain.TenantID
}

// GetSuccessFeeHandler — use case чтения вознаграждения.
type GetSuccessFeeHandler struct {
	fees ports.SuccessFeeRepository
}

// NewGetSuccessFeeHandler собирает use case чтения.
func NewGetSuccessFeeHandler(fees ports.SuccessFeeRepository) *GetSuccessFeeHandler {
	return &GetSuccessFeeHandler{fees: fees}
}

// Handle загружает вознаграждение и строит представление. Вознаграждение чужого
// тенанта неотличимо от отсутствующего.
func (h *GetSuccessFeeHandler) Handle(ctx context.Context, q GetSuccessFee) (SuccessFeeView, error) {
	fee, err := h.fees.FindByID(ctx, q.SuccessFeeID)
	if err != nil {
		return SuccessFeeView{}, err
	}
	if fee.TenantID() != q.TenantID {
		return SuccessFeeView{}, fmt.Errorf("%w: %s", ports.ErrSuccessFeeNotFound, q.SuccessFeeID)
	}
	return buildSuccessFeeView(fee), nil
}

// buildSuccessFeeView переводит агрегат в представление чтения.
func buildSuccessFeeView(fee *domain.SuccessFee) SuccessFeeView {
	return SuccessFeeView{
		SuccessFeeID:   fee.ID().String(),
		TenantID:       fee.TenantID().String(),
		SubscriptionID: fee.SubscriptionID().String(),
		ResultRef:      fee.ResultRef().String(),
		DealID:         fee.DealID().String(),
		BaseMinor:      fee.Base().AmountMinor(),
		Currency:       fee.Base().Currency(),
		RateBps:        fee.RateBps(),
		AmountMinor:    fee.Amount().AmountMinor(),
		Calculation:    fee.Calculation().String(),
		Status:         fee.Status().String(),
		Version:        fee.Version(),
	}
}

// ListSuccessFees — запрос перечня вознаграждений тенанта.
type ListSuccessFees struct {
	TenantID domain.TenantID
}

// ListSuccessFeesHandler — use case чтения перечня вознаграждений.
type ListSuccessFeesHandler struct {
	fees ports.SuccessFeeRepository
}

// NewListSuccessFeesHandler собирает use case.
func NewListSuccessFeesHandler(fees ports.SuccessFeeRepository) *ListSuccessFeesHandler {
	return &ListSuccessFeesHandler{fees: fees}
}

// Handle возвращает вознаграждения тенанта, упорядоченные по идентификатору.
func (h *ListSuccessFeesHandler) Handle(ctx context.Context, q ListSuccessFees) ([]SuccessFeeView, error) {
	fees, err := h.fees.ListByTenant(ctx, q.TenantID)
	if err != nil {
		return nil, err
	}
	views := make([]SuccessFeeView, 0, len(fees))
	for _, fee := range fees {
		views = append(views, buildSuccessFeeView(fee))
	}
	return views, nil
}
