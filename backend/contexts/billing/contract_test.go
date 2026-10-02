package billing_test

import (
	"context"
	"errors"
	"testing"

	billing "lead-gen/backend/contexts/billing"
	"lead-gen/backend/contexts/billing/internal/app/command"
	"lead-gen/contracts/billing/billingentitlement"
	"lead-gen/contracts/billing/usagerecord"
)

func validUsageRecord(tenantID, usageKey, feature string) usagerecord.UsageRecord {
	return usagerecord.UsageRecord{
		Envelope: usagerecord.Envelope{
			EventID:       "evt-usage",
			OccurredAt:    testNow,
			TenantID:      tenantID,
			CorrelationID: "corr-1",
			Name:          usagerecord.ContractName,
			SchemaVersion: usagerecord.SchemaVersion,
		},
		RecordID:  "rec-" + usageKey,
		Feature:   feature,
		Quantity:  1,
		PeriodKey: "p1",
		UsageKey:  usageKey,
	}
}

// TestUsageRecordContract проверяет контракт usage-record: приём записи,
// идемпотентность повтора и доменный отказ.
func TestUsageRecordContract(t *testing.T) {
	ctx := context.Background()
	slice, _ := newSlice(t, &fixedClock{now: testNow})
	if _, err := slice.ActivateSubscription.Handle(ctx, command.ActivateSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-1", Tariff: testTariff(t), Period: testPeriod(t),
	}); err != nil {
		t.Fatalf("активация: %v", err)
	}

	port := billing.NewUsageRecordPort(slice)
	ack, err := port.Record(ctx, validUsageRecord("tenant-1", "u1", "outreach-channel-email"))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !ack.Accepted || ack.Duplicate || ack.Remaining != 9 {
		t.Fatalf("подтверждение = %+v", ack)
	}

	duplicate, err := port.Record(ctx, validUsageRecord("tenant-1", "u1", "outreach-channel-email"))
	if err != nil {
		t.Fatalf("повтор Record: %v", err)
	}
	if !duplicate.Duplicate {
		t.Fatalf("повтор = %+v", duplicate)
	}

	if _, err := port.Record(ctx, validUsageRecord("tenant-1", "u2", "unknown-feature")); !errors.Is(err, usagerecord.ErrRejected) {
		t.Fatalf("возможность вне тарифа: ошибка = %v", err)
	}
	if _, err := port.Record(ctx, validUsageRecord("tenant-ghost", "u3", "outreach-channel-email")); !errors.Is(err, usagerecord.ErrRejected) {
		t.Fatalf("подписка не найдена: ошибка = %v", err)
	}

	nilPort := billing.NewUsageRecordPort(nil)
	if _, err := nilPort.Record(ctx, validUsageRecord("tenant-1", "u4", "outreach-channel-email")); !errors.Is(err, usagerecord.ErrUnavailable) {
		t.Fatalf("срез не собран: ошибка = %v", err)
	}
}

// TestBillingEntitlementContract проверяет контракт billing-entitlement: чтение
// прав и лимитов тенанта и отказ по неизвестной подписке.
func TestBillingEntitlementContract(t *testing.T) {
	ctx := context.Background()
	slice, _ := newSlice(t, &fixedClock{now: testNow})
	if _, err := slice.ActivateSubscription.Handle(ctx, command.ActivateSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-1", Tariff: testTariff(t), Period: testPeriod(t),
	}); err != nil {
		t.Fatalf("активация: %v", err)
	}

	port := billing.NewBillingEntitlement(slice)
	entitlement, err := port.Entitlement(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Entitlement: %v", err)
	}
	if err := entitlement.Validate(); err != nil {
		t.Fatalf("права невалидны: %v", err)
	}
	if entitlement.Status != "active" || len(entitlement.Limits) != 2 {
		t.Fatalf("права = %+v", entitlement)
	}
	if remaining, ok := entitlement.Remaining("outreach-channel-email"); !ok || remaining != 10 {
		t.Fatalf("остаток лимита = %d, ok=%v", remaining, ok)
	}
	if entitlement.Allows("unknown-feature") {
		t.Fatal("возможность вне тарифа не должна быть разрешена")
	}

	if _, err := port.Entitlement(ctx, "tenant-ghost"); !errors.Is(err, billingentitlement.ErrNotFound) {
		t.Fatalf("неизвестный тенант: ошибка = %v", err)
	}

	nilPort := billing.NewBillingEntitlement(nil)
	if _, err := nilPort.Entitlement(ctx, "tenant-1"); !errors.Is(err, billingentitlement.ErrUnavailable) {
		t.Fatalf("срез не собран: ошибка = %v", err)
	}
}
