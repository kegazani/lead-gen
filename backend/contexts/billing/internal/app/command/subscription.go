package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// ActivateSubscription — команда активации подписки на тариф. Если подписки ещё
// нет, она создаётся активной на оплаченный период; приостановленная подписка
// возобновляется. Счёт за период выставляется в той же транзакции.
type ActivateSubscription struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
	Tariff         domain.Tariff
	Period         domain.BillingPeriod
	At             time.Time
	CorrelationID  domain.CorrelationID
	CausationID    domain.CausationID
}

// ActivateSubscriptionResult — результат активации подписки.
type ActivateSubscriptionResult struct {
	SubscriptionID domain.SubscriptionID
	Status         domain.SubscriptionStatus
	Version        int
}

// ActivateSubscriptionHandler — use case активации подписки.
type ActivateSubscriptionHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
}

// NewActivateSubscriptionHandler собирает use case.
func NewActivateSubscriptionHandler(uow ports.UnitOfWork, clock ports.Clock) *ActivateSubscriptionHandler {
	return &ActivateSubscriptionHandler{uow: uow, clock: clock}
}

// Handle создаёт или возобновляет подписку, открывая доступ к функциям тарифа на
// оплаченный период. Счёт за период выставляет расчётный контур или явная команда
// IssueInvoice.
func (h *ActivateSubscriptionHandler) Handle(ctx context.Context, cmd ActivateSubscription) (ActivateSubscriptionResult, error) {
	at := atOrNow(cmd.At, h.clock)
	var result ActivateSubscriptionResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		subscription, err := tx.Subscriptions().FindByID(ctx, cmd.SubscriptionID)
		created := false
		switch {
		case err == nil:
			if subscription.TenantID() != cmd.TenantID {
				return fmt.Errorf("%w: подписка %s", ErrTenantMismatch, cmd.SubscriptionID)
			}
		case errors.Is(err, ports.ErrSubscriptionNotFound):
			subscription, err = domain.NewSubscription(domain.NewSubscriptionParams{
				ID:       cmd.SubscriptionID,
				TenantID: cmd.TenantID,
				Tariff:   cmd.Tariff,
				Period:   cmd.Period,
			})
			if err != nil {
				return err
			}
			created = true
		default:
			return err
		}

		expected := 0
		if !created {
			expected = subscription.Version()
			if !subscription.IsActive() {
				if err := subscription.Resume(at); err != nil {
					return err
				}
			}
		}

		if err := tx.Subscriptions().Save(ctx, subscription, expected); err != nil {
			return err
		}
		result = ActivateSubscriptionResult{
			SubscriptionID: subscription.ID(),
			Status:         subscription.Status(),
			Version:        subscription.Version(),
		}
		return nil
	})
	if err != nil {
		return ActivateSubscriptionResult{}, err
	}
	return result, nil
}

// SuspendSubscription — команда приостановки подписки при неоплате.
type SuspendSubscription struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
	At             time.Time
}

// SuspendSubscriptionHandler — use case приостановки подписки.
type SuspendSubscriptionHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
}

// NewSuspendSubscriptionHandler собирает use case.
func NewSuspendSubscriptionHandler(uow ports.UnitOfWork, clock ports.Clock) *SuspendSubscriptionHandler {
	return &SuspendSubscriptionHandler{uow: uow, clock: clock}
}

// Handle приостанавливает доступ, сохраняя данные клиента.
func (h *SuspendSubscriptionHandler) Handle(ctx context.Context, cmd SuspendSubscription) (SubscriptionResult, error) {
	at := atOrNow(cmd.At, h.clock)
	return changeSubscription(ctx, h.uow, cmd.SubscriptionID, cmd.TenantID,
		func(subscription *domain.SubscriptionInvoice) ([]domain.DomainEvent, error) {
			return nil, subscription.Suspend(at)
		})
}

// ResumeSubscription — команда возобновления подписки после оплаты.
type ResumeSubscription struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
	At             time.Time
}

// ResumeSubscriptionHandler — use case возобновления подписки.
type ResumeSubscriptionHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
}

// NewResumeSubscriptionHandler собирает use case.
func NewResumeSubscriptionHandler(uow ports.UnitOfWork, clock ports.Clock) *ResumeSubscriptionHandler {
	return &ResumeSubscriptionHandler{uow: uow, clock: clock}
}

// Handle возобновляет доступ в полном объёме тарифа.
func (h *ResumeSubscriptionHandler) Handle(ctx context.Context, cmd ResumeSubscription) (SubscriptionResult, error) {
	at := atOrNow(cmd.At, h.clock)
	return changeSubscription(ctx, h.uow, cmd.SubscriptionID, cmd.TenantID,
		func(subscription *domain.SubscriptionInvoice) ([]domain.DomainEvent, error) {
			return nil, subscription.Resume(at)
		})
}

// IssueInvoice — команда выставления счёта за период.
type IssueInvoice struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
	Period         domain.BillingPeriod
	At             time.Time
	CorrelationID  domain.CorrelationID
	CausationID    domain.CausationID
}

// IssueInvoiceResult — результат выставления счёта.
type IssueInvoiceResult struct {
	SubscriptionID domain.SubscriptionID
	InvoiceID      domain.InvoiceID
	Amount         domain.Money
	Version        int
	Events         []domain.DomainEvent
}

// IssueInvoiceHandler — use case выставления счёта.
type IssueInvoiceHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewIssueInvoiceHandler собирает use case.
func NewIssueInvoiceHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *IssueInvoiceHandler {
	return &IssueInvoiceHandler{uow: uow, clock: clock, ids: ids}
}

// Handle выставляет счёт за период в пределах активной подписки и её лимитов.
func (h *IssueInvoiceHandler) Handle(ctx context.Context, cmd IssueInvoice) (IssueInvoiceResult, error) {
	at := atOrNow(cmd.At, h.clock)
	var result IssueInvoiceResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		subscription, err := tx.Subscriptions().FindByID(ctx, cmd.SubscriptionID)
		if err != nil {
			return err
		}
		if subscription.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: подписка %s", ErrTenantMismatch, cmd.SubscriptionID)
		}
		expected := subscription.Version()
		invoice, events, err := subscription.IssueInvoice(domain.InvoicingInput{
			Period:        cmd.Period,
			At:            at,
			EventID:       h.ids.NewEventID(),
			CorrelationID: cmd.CorrelationID,
			CausationID:   cmd.CausationID,
		})
		if err != nil {
			return err
		}
		if err := tx.Subscriptions().Save(ctx, subscription, expected); err != nil {
			return err
		}
		if len(events) > 0 {
			if err := tx.Outbox().Append(ctx, events...); err != nil {
				return err
			}
		}
		subscription.PullDomainEvents()
		result = IssueInvoiceResult{
			SubscriptionID: subscription.ID(),
			InvoiceID:      invoice.ID(),
			Amount:         invoice.Amount(),
			Version:        subscription.Version(),
			Events:         events,
		}
		return nil
	})
	if err != nil {
		return IssueInvoiceResult{}, err
	}
	return result, nil
}
