package command

import (
	"context"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// SettleDuePeriods — сценарий фонового расчётного контура роли worker: по активным
// подпискам выставляет недостающие счета за текущий период и открывает следующий
// период, когда текущий завершился. Работа идемпотентна: уже выставленный счёт не
// выставляется повторно.
type SettleDuePeriods struct{}

// SettleDuePeriodsResult — итог прохода расчётного контура.
type SettleDuePeriodsResult struct {
	Processed      int
	InvoicesIssued int
	Renewed        int
	Events         []domain.DomainEvent
}

// SettleDuePeriodsHandler — use case расчётного контура.
type SettleDuePeriodsHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewSettleDuePeriodsHandler собирает use case.
func NewSettleDuePeriodsHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *SettleDuePeriodsHandler {
	return &SettleDuePeriodsHandler{uow: uow, clock: clock, ids: ids}
}

// Handle выполняет один проход по всем подпискам в одной транзакции.
func (h *SettleDuePeriodsHandler) Handle(ctx context.Context) (SettleDuePeriodsResult, error) {
	now := h.clock.Now()
	var result SettleDuePeriodsResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		subscriptions, err := tx.Subscriptions().List(ctx)
		if err != nil {
			return err
		}
		var allEvents []domain.DomainEvent
		for _, subscription := range subscriptions {
			if !subscription.IsActive() {
				continue
			}
			result.Processed++
			expected := subscription.Version()
			changed := false

			if _, ok := subscription.InvoiceForPeriod(subscription.Period()); !ok {
				_, events, err := subscription.IssueInvoice(domain.InvoicingInput{
					Period:  subscription.Period(),
					At:      now,
					EventID: h.ids.NewEventID(),
				})
				if err != nil {
					return err
				}
				if len(events) > 0 {
					result.InvoicesIssued++
					allEvents = append(allEvents, events...)
					changed = true
				}
			}

			if !subscription.Period().End().After(now) {
				next, err := domain.NewBillingPeriod(subscription.Period().End(), subscription.Period().End().Add(subscription.Period().Duration()))
				if err != nil {
					return err
				}
				if err := subscription.RenewPeriod(next); err != nil {
					return err
				}
				result.Renewed++
				changed = true
			}

			if changed {
				if err := tx.Subscriptions().Save(ctx, subscription, expected); err != nil {
					return err
				}
			}
			subscription.PullDomainEvents()
		}
		if len(allEvents) > 0 {
			if err := tx.Outbox().Append(ctx, allEvents...); err != nil {
				return err
			}
		}
		result.Events = allEvents
		return nil
	})
	if err != nil {
		return SettleDuePeriodsResult{}, err
	}
	return result, nil
}
