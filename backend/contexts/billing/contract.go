package billing

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/billing/internal/app/command"
	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
	"lead-gen/contracts/billing/billingentitlement"
	"lead-gen/contracts/billing/usagerecord"
)

// NewUsageRecordPort собирает опубликованный контракт usage-record поверх
// собранного среза billing: запись потребления тенанта.
//
// Это публичная фабрика владельца тарификации: точка сборки (cmd/lead-gen) не
// имеет доступа к внутренним пакетам контекста, поэтому прямой адаптер контракта
// объявлен здесь. При co-location он вызывается в процессе; при разделении
// сервисов та же форма даётся сетевым клиентом.
func NewUsageRecordPort(slice *Slice) usagerecord.Port {
	if slice == nil {
		return usagerecord.InProc(usagerecord.InProcHandler{})
	}
	return usagerecord.InProc(usagerecord.InProcHandler{
		RecordFunc: func(ctx context.Context, record usagerecord.UsageRecord) (usagerecord.Ack, error) {
			subscription, err := slice.SubscriptionRepository.FindByTenant(ctx, domain.TenantID(record.TenantID))
			if err != nil {
				return usagerecord.Ack{}, usageRecordError(err)
			}
			result, err := slice.RecordUsage.Handle(ctx, command.RecordUsage{
				SubscriptionID: subscription.ID(),
				TenantID:       domain.TenantID(record.TenantID),
				UsageID:        domain.UsageID(record.RecordID),
				Feature:        domain.Feature(record.Feature),
				Quantity:       record.Quantity,
				UsageKey:       record.UsageKey,
				CorrelationID:  domain.CorrelationID(record.CorrelationID),
			})
			if err != nil {
				return usagerecord.Ack{}, usageRecordError(err)
			}
			return usagerecord.Ack{
				Accepted:      true,
				Duplicate:     result.Duplicate,
				Remaining:     result.Remaining,
				InvoiceIssued: result.InvoiceIssued,
				InvoiceID:     result.InvoiceID.String(),
			}, nil
		},
	})
}

// usageRecordError переводит ошибку сценария фиксации в исход опубликованного
// контракта: доменный отказ и отсутствие подписки — ErrRejected, прочее —
// ErrUnavailable. Ошибка не выдаётся за успех.
func usageRecordError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrSubscriptionNotFound),
		errors.Is(err, domain.ErrFeatureNotInTariff),
		errors.Is(err, domain.ErrTariffLimitExceeded),
		errors.Is(err, domain.ErrSubscriptionNotActive),
		errors.Is(err, domain.ErrUnknownFeature),
		errors.Is(err, domain.ErrQuantityRequired),
		errors.Is(err, domain.ErrUsageKeyRequired),
		errors.Is(err, domain.ErrEmptyID):
		return fmt.Errorf("%w: %v", usagerecord.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", usagerecord.ErrUnavailable, err)
	}
}

// NewBillingEntitlement собирает опубликованный контракт billing-entitlement
// поверх собранного среза billing: чтение прав и лимитов тенанта.
//
// При co-location вызов идёт в процессе; при разделении сервисов policy
// доставляется подпиской на изменение правила, а та же форма даётся сетевым
// клиентом.
func NewBillingEntitlement(slice *Slice) billingentitlement.Port {
	if slice == nil {
		return billingentitlement.InProc(billingentitlement.InProcHandler{})
	}
	return billingentitlement.InProc(billingentitlement.InProcHandler{
		EntitlementFunc: func(ctx context.Context, tenantID string) (billingentitlement.Entitlement, error) {
			subscription, err := slice.SubscriptionRepository.FindByTenant(ctx, domain.TenantID(tenantID))
			if err != nil {
				return billingentitlement.Entitlement{}, entitlementError(err)
			}
			limits := make([]billingentitlement.Limit, 0)
			for _, limit := range subscription.Tariff().Limits() {
				limits = append(limits, billingentitlement.Limit{
					Feature:   limit.Feature().String(),
					Limit:     limit.Limit(),
					Used:      limit.Used(),
					Remaining: limit.Remaining(),
				})
			}
			return billingentitlement.Entitlement{
				Envelope: billingentitlement.Envelope{
					EventID:       string(slice.ids.NewEventID()),
					OccurredAt:    slice.clock.Now(),
					TenantID:      tenantID,
					CorrelationID: tenantID,
					Name:          billingentitlement.ContractName,
					SchemaVersion: billingentitlement.SchemaVersion,
				},
				SubscriptionID: subscription.ID().String(),
				TariffID:       subscription.Tariff().ID().String(),
				Status:         subscription.Status().String(),
				PeriodStart:    subscription.Period().Start(),
				PeriodEnd:      subscription.Period().End(),
				Limits:         limits,
			}, nil
		},
	})
}

// entitlementError переводит ошибку чтения прав в исход контракта: отсутствие
// подписки — ErrNotFound, прочее — ErrUnavailable.
func entitlementError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrSubscriptionNotFound):
		return fmt.Errorf("%w: %v", billingentitlement.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", billingentitlement.ErrUnavailable, err)
	}
}
