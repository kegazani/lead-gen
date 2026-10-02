package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// GetEntitlement — запрос прав и лимитов подписки (контракт billing-entitlement).
type GetEntitlement struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
}

// EntitlementView — представление прав и лимитов подписки.
type EntitlementView struct {
	SubscriptionID string      `json:"subscription_id"`
	TenantID       string      `json:"tenant_id"`
	TariffID       string      `json:"tariff_id"`
	Status         string      `json:"status"`
	PeriodStart    string      `json:"period_start"`
	PeriodEnd      string      `json:"period_end"`
	Limits         []LimitView `json:"limits"`
	Version        int         `json:"version"`
}

// GetEntitlementHandler — use case чтения прав и лимитов.
type GetEntitlementHandler struct {
	subscriptions ports.SubscriptionRepository
}

// NewGetEntitlementHandler собирает use case чтения прав.
func NewGetEntitlementHandler(subscriptions ports.SubscriptionRepository) *GetEntitlementHandler {
	return &GetEntitlementHandler{subscriptions: subscriptions}
}

// Handle загружает подписку и строит представление прав. Подписка другого тенанта
// неотличима от отсутствующей.
func (h *GetEntitlementHandler) Handle(ctx context.Context, q GetEntitlement) (EntitlementView, error) {
	subscription, err := h.subscriptions.FindByID(ctx, q.SubscriptionID)
	if err != nil {
		return EntitlementView{}, err
	}
	if subscription.TenantID() != q.TenantID {
		return EntitlementView{}, fmt.Errorf("%w: %s", ports.ErrSubscriptionNotFound, q.SubscriptionID)
	}
	view := EntitlementView{
		SubscriptionID: subscription.ID().String(),
		TenantID:       subscription.TenantID().String(),
		TariffID:       subscription.Tariff().ID().String(),
		Status:         subscription.Status().String(),
		PeriodStart:    subscription.Period().Start().UTC().Format("2006-01-02T15:04:05Z07:00"),
		PeriodEnd:      subscription.Period().End().UTC().Format("2006-01-02T15:04:05Z07:00"),
		Version:        subscription.Version(),
	}
	for _, limit := range subscription.Tariff().Limits() {
		view.Limits = append(view.Limits, LimitView{
			Feature:   limit.Feature().String(),
			Limit:     limit.Limit(),
			Used:      limit.Used(),
			Remaining: limit.Remaining(),
		})
	}
	return view, nil
}
