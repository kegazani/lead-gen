package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// RecordUsage — команда фиксации потребления возможности за период (контракт
// usage-record). Запись идемпотентна по ключу периода; политика
// subscription-entitlement проверяет вхождение возможности в тариф и лимит и
// решает, пора ли выставить счёт за исчерпанный период.
type RecordUsage struct {
	SubscriptionID domain.SubscriptionID
	TenantID       domain.TenantID
	UsageID        domain.UsageID
	Feature        domain.Feature
	Quantity       int64
	UsageKey       string
	At             time.Time
	CorrelationID  domain.CorrelationID
	CausationID    domain.CausationID
}

// RecordUsageResult — результат фиксации потребления.
type RecordUsageResult struct {
	SubscriptionID domain.SubscriptionID
	Recorded       bool
	Duplicate      bool
	Feature        domain.Feature
	Quantity       int64
	Limit          int64
	Remaining      int64
	InvoiceIssued  bool
	InvoiceID      domain.InvoiceID
	Version        int
	Events         []domain.DomainEvent
}

// RecordUsageHandler — use case фиксации потребления.
type RecordUsageHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewRecordUsageHandler собирает use case.
func NewRecordUsageHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *RecordUsageHandler {
	return &RecordUsageHandler{uow: uow, clock: clock, ids: ids}
}

// Handle фиксирует потребление и, если политика признала период исчерпанным,
// выставляет счёт — всё в одной транзакции.
func (h *RecordUsageHandler) Handle(ctx context.Context, cmd RecordUsage) (RecordUsageResult, error) {
	at := atOrNow(cmd.At, h.clock)
	var result RecordUsageResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		subscription, err := tx.Subscriptions().FindByID(ctx, cmd.SubscriptionID)
		if err != nil {
			return err
		}
		if subscription.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: подписка %s", ErrTenantMismatch, cmd.SubscriptionID)
		}
		expected := subscription.Version()

		// Идемпотентность: повторная запись не меняет состояние и не расходует
		// лимит дважды.
		if subscription.HasUsage(cmd.UsageKey) {
			limit, _ := findLimit(subscription.Tariff(), cmd.Feature)
			result = RecordUsageResult{
				SubscriptionID: subscription.ID(),
				Duplicate:      true,
				Feature:        cmd.Feature,
				Limit:          limit.Limit(),
				Remaining:      limit.Remaining(),
				Version:        subscription.Version(),
			}
			return nil
		}

		limit, _ := findLimit(subscription.Tariff(), cmd.Feature)
		_, alreadyInvoiced := subscription.InvoiceForPeriod(subscription.Period())
		decision, err := domain.SubscriptionEntitlement(
			subscription.Tariff(), cmd.Feature, cmd.Quantity, limit.Used(), alreadyInvoiced,
		)
		if err != nil {
			return err
		}
		if !decision.FeatureEntitled {
			return fmt.Errorf("%w: %q", domain.ErrFeatureNotInTariff, cmd.Feature)
		}
		if !decision.Allowed {
			return fmt.Errorf(
				"%w: возможность %q: остаток %d, запрошено %d",
				domain.ErrTariffLimitExceeded, cmd.Feature, limit.Remaining(), cmd.Quantity,
			)
		}

		usage, err := subscription.RecordUsage(domain.UsageInput{
			UsageID:  cmd.UsageID,
			Feature:  cmd.Feature,
			Quantity: cmd.Quantity,
			UsageKey: cmd.UsageKey,
			At:       at,
		})
		if err != nil {
			return err
		}

		var events []domain.DomainEvent
		var invoiceID domain.InvoiceID
		invoiceIssued := false
		if decision.ShouldIssueInvoice {
			invoice, issued, err := subscription.IssueInvoice(domain.InvoicingInput{
				Period:        subscription.Period(),
				At:            at,
				EventID:       h.ids.NewEventID(),
				CorrelationID: cmd.CorrelationID,
				CausationID:   cmd.CausationID,
			})
			if err != nil {
				return err
			}
			invoiceID = invoice.ID()
			invoiceIssued = len(issued) > 0
			events = append(events, issued...)
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
		result = RecordUsageResult{
			SubscriptionID: subscription.ID(),
			Recorded:       true,
			Feature:        usage.Feature,
			Quantity:       usage.Quantity,
			Limit:          usage.Limit,
			Remaining:      usage.Remaining,
			InvoiceIssued:  invoiceIssued,
			InvoiceID:      invoiceID,
			Version:        subscription.Version(),
			Events:         events,
		}
		return nil
	})
	if err != nil {
		return RecordUsageResult{}, err
	}
	return result, nil
}
