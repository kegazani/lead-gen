package query

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// LimitView — представление тарифного лимита.
type LimitView struct {
	Feature   string `json:"feature"`
	Limit     int64  `json:"limit"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining"`
}

// UsageView — представление записи потребления.
type UsageView struct {
	UsageID  string    `json:"usage_id"`
	Feature  string    `json:"feature"`
	Quantity int64     `json:"quantity"`
	UsageKey string    `json:"usage_key"`
	At       time.Time `json:"at"`
}

// InvoiceView — представление счёта.
type InvoiceView struct {
	InvoiceID   string    `json:"invoice_id"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	IssuedAt    time.Time `json:"issued_at"`
	Paid        bool      `json:"paid"`
	PaidAt      time.Time `json:"paid_at,omitempty"`
	PaymentRef  string    `json:"payment_ref,omitempty"`
}

// SubscriptionView — представление подписки для чтения: детализация начислений и
// потребления (BRD, прозрачность расчёта).
type SubscriptionView struct {
	SubscriptionID string        `json:"subscription_id"`
	TenantID       string        `json:"tenant_id"`
	Status         string        `json:"status"`
	TariffID       string        `json:"tariff_id"`
	Version        int           `json:"version"`
	PeriodStart    time.Time     `json:"period_start"`
	PeriodEnd      time.Time     `json:"period_end"`
	Limits         []LimitView   `json:"limits"`
	Usage          []UsageView   `json:"usage"`
	Invoices       []InvoiceView `json:"invoices"`
}

// GetSubscription — запрос подписки по идентификатору.
type GetSubscription struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
}

// GetSubscriptionHandler — use case чтения подписки.
type GetSubscriptionHandler struct {
	subscriptions ports.SubscriptionRepository
}

// NewGetSubscriptionHandler собирает use case чтения.
func NewGetSubscriptionHandler(subscriptions ports.SubscriptionRepository) *GetSubscriptionHandler {
	return &GetSubscriptionHandler{subscriptions: subscriptions}
}

// Handle загружает подписку и строит представление. Подписка другого тенанта
// неотличима от отсутствующей.
func (h *GetSubscriptionHandler) Handle(ctx context.Context, q GetSubscription) (SubscriptionView, error) {
	subscription, err := h.subscriptions.FindByID(ctx, q.SubscriptionID)
	if err != nil {
		return SubscriptionView{}, err
	}
	if subscription.TenantID() != q.TenantID {
		return SubscriptionView{}, fmt.Errorf("%w: %s", ports.ErrSubscriptionNotFound, q.SubscriptionID)
	}
	return buildSubscriptionView(subscription), nil
}

// buildSubscriptionView переводит агрегат в представление чтения.
func buildSubscriptionView(subscription *domain.SubscriptionInvoice) SubscriptionView {
	view := SubscriptionView{
		SubscriptionID: subscription.ID().String(),
		TenantID:       subscription.TenantID().String(),
		Status:         subscription.Status().String(),
		TariffID:       subscription.Tariff().ID().String(),
		Version:        subscription.Version(),
		PeriodStart:    subscription.Period().Start(),
		PeriodEnd:      subscription.Period().End(),
	}
	for _, limit := range subscription.Tariff().Limits() {
		view.Limits = append(view.Limits, LimitView{
			Feature:   limit.Feature().String(),
			Limit:     limit.Limit(),
			Used:      limit.Used(),
			Remaining: limit.Remaining(),
		})
	}
	for _, entry := range subscription.Usage() {
		view.Usage = append(view.Usage, UsageView{
			UsageID:  entry.ID().String(),
			Feature:  entry.Feature().String(),
			Quantity: entry.Quantity(),
			UsageKey: entry.UsageKey(),
			At:       entry.At(),
		})
	}
	for _, invoice := range subscription.Invoices() {
		view.Invoices = append(view.Invoices, InvoiceView{
			InvoiceID:   invoice.ID().String(),
			PeriodStart: invoice.Period().Start(),
			PeriodEnd:   invoice.Period().End(),
			AmountMinor: invoice.Amount().AmountMinor(),
			Currency:    invoice.Amount().Currency(),
			IssuedAt:    invoice.IssuedAt(),
			Paid:        invoice.Paid(),
			PaidAt:      invoice.PaidAt(),
			PaymentRef:  invoice.PaymentRef(),
		})
	}
	return view
}
