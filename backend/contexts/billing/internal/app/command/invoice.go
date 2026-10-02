package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// ChargeInvoice — команда списания по счёту через внешнюю платёжную систему.
type ChargeInvoice struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
	InvoiceID      domain.InvoiceID
	At             time.Time
}

// ChargeInvoiceResult — результат списания.
type ChargeInvoiceResult struct {
	SubscriptionID domain.SubscriptionID
	InvoiceID      domain.InvoiceID
	Amount         domain.Money
	Paid           bool
	Reference      string
	Version        int
}

// ChargeInvoiceHandler — use case списания по счёту.
type ChargeInvoiceHandler struct {
	uow     ports.UnitOfWork
	gateway ports.PaymentGateway
	clock   ports.Clock
}

// NewChargeInvoiceHandler собирает use case. Платёжный порт обязателен: без него
// списание честно отвечает «недоступно».
func NewChargeInvoiceHandler(uow ports.UnitOfWork, gateway ports.PaymentGateway, clock ports.Clock) *ChargeInvoiceHandler {
	return &ChargeInvoiceHandler{uow: uow, gateway: gateway, clock: clock}
}

// Handle списывает сумму счёта и отмечает его оплаченным. Недоступность платёжной
// системы и её отказ не выдаются за успех: счёт остаётся неоплаченным, транзакция
// откатывается.
func (h *ChargeInvoiceHandler) Handle(ctx context.Context, cmd ChargeInvoice) (ChargeInvoiceResult, error) {
	at := atOrNow(cmd.At, h.clock)
	var result ChargeInvoiceResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		subscription, err := tx.Subscriptions().FindByID(ctx, cmd.SubscriptionID)
		if err != nil {
			return err
		}
		if subscription.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: подписка %s", ErrTenantMismatch, cmd.SubscriptionID)
		}
		invoice, ok := subscription.Invoice(cmd.InvoiceID)
		if !ok {
			return fmt.Errorf("%w: %s", domain.ErrInvoiceNotFound, cmd.InvoiceID)
		}

		expected := subscription.Version()
		reference := invoice.PaymentRef()
		if !invoice.Paid() {
			if h.gateway == nil {
				return ports.ErrPaymentUnavailable
			}
			charge, err := h.gateway.Charge(ctx, ports.ChargeRequest{
				TenantID:       cmd.TenantID,
				SubscriptionID: cmd.SubscriptionID,
				InvoiceID:      invoice.ID(),
				Amount:         invoice.Amount(),
				IdempotencyKey: "invoice:" + string(invoice.ID()),
			})
			if err != nil {
				return err
			}
			paid, err := subscription.MarkInvoicePaid(invoice.ID(), at, charge.Reference)
			if err != nil {
				return err
			}
			invoice = paid
			reference = charge.Reference
			if err := tx.Subscriptions().Save(ctx, subscription, expected); err != nil {
				return err
			}
		}

		result = ChargeInvoiceResult{
			SubscriptionID: subscription.ID(),
			InvoiceID:      invoice.ID(),
			Amount:         invoice.Amount(),
			Paid:           invoice.Paid(),
			Reference:      reference,
			Version:        subscription.Version(),
		}
		return nil
	})
	if err != nil {
		return ChargeInvoiceResult{}, err
	}
	return result, nil
}
