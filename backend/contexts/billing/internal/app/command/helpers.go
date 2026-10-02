package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// SubscriptionResult — результат изменения подписки: новая версия агрегата и
// события, записанные в outbox в той же транзакции.
type SubscriptionResult struct {
	SubscriptionID domain.SubscriptionID
	Status         domain.SubscriptionStatus
	Version        int
	Events         []domain.DomainEvent
}

// changeSubscription выполняет изменение существующей подписки в одной
// транзакции: загружает агрегат, сверяет тенант, фиксирует ожидаемую версию,
// применяет изменение, сохраняет с оптимистичной блокировкой и пишет события в
// outbox. Ошибка изменения откатывает транзакцию.
func changeSubscription(
	ctx context.Context,
	uow ports.UnitOfWork,
	subscriptionID domain.SubscriptionID,
	tenantID domain.TenantID,
	change func(subscription *domain.SubscriptionInvoice) ([]domain.DomainEvent, error),
) (SubscriptionResult, error) {
	var result SubscriptionResult
	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		subscription, err := tx.Subscriptions().FindByID(ctx, subscriptionID)
		if err != nil {
			return err
		}
		if subscription.TenantID() != tenantID {
			return fmt.Errorf("%w: подписка %s", ErrTenantMismatch, subscriptionID)
		}
		expected := subscription.Version()
		events, err := change(subscription)
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
		result = SubscriptionResult{
			SubscriptionID: subscription.ID(),
			Status:         subscription.Status(),
			Version:        subscription.Version(),
			Events:         events,
		}
		return nil
	})
	if err != nil {
		return SubscriptionResult{}, err
	}
	return result, nil
}

// atOrNow возвращает заданное время либо время часов.
func atOrNow(at time.Time, clock ports.Clock) time.Time {
	if !at.IsZero() {
		return at
	}
	if clock != nil {
		return clock.Now()
	}
	return time.Time{}
}

// findLimit ищет тарифный лимит возможности в тарифе.
func findLimit(tariff domain.Tariff, feature domain.Feature) (domain.TariffLimit, bool) {
	for _, limit := range tariff.Limits() {
		if limit.Feature() == feature {
			return limit, true
		}
	}
	return domain.TariffLimit{}, false
}
