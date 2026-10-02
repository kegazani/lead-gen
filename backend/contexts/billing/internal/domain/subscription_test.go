package domain

import (
	"errors"
	"testing"
	"time"
)

func newTestSubscription(t *testing.T, emailLimit int64) *SubscriptionInvoice {
	t.Helper()
	tariff := newTestTariff(t, emailLimit, 1000)
	period, err := NewBillingPeriod(testNow, testNow.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("период: %v", err)
	}
	subscription, err := NewSubscription(NewSubscriptionParams{
		ID:       "sub-1",
		TenantID: "tenant-1",
		Tariff:   tariff,
		Period:   period,
	})
	if err != nil {
		t.Fatalf("подписка: %v", err)
	}
	return subscription
}

func TestSubscriptionRecordUsageIsIdempotentByPeriodKey(t *testing.T) {
	subscription := newTestSubscription(t, 10)
	email, _ := NewFeature("outreach-channel-email")

	first, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-1", Feature: email, Quantity: 1, UsageKey: "u1", At: testNow,
	})
	if err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if first.Duplicate || first.Remaining != 9 {
		t.Fatalf("первая запись = %+v", first)
	}

	repeat, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-2", Feature: email, Quantity: 1, UsageKey: "u1", At: testNow,
	})
	if err != nil {
		t.Fatalf("повтор RecordUsage: %v", err)
	}
	if !repeat.Duplicate || repeat.Remaining != 9 {
		t.Fatalf("повтор должен быть дубликатом без расхода лимита: %+v", repeat)
	}
	if len(subscription.Usage()) != 1 {
		t.Fatalf("записей потребления = %d, ожидалось 1", len(subscription.Usage()))
	}

	if _, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-3", Feature: email, Quantity: 20, UsageKey: "u2", At: testNow,
	}); !errors.Is(err, ErrTariffLimitExceeded) {
		t.Fatalf("превышение лимита: ошибка = %v", err)
	}

	enrichment, _ := NewFeature("osint-enrichment")
	if _, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-4", Feature: enrichment, Quantity: 1, UsageKey: "u3", At: testNow,
	}); err != nil {
		t.Fatalf("возможность тарифа должна приниматься: %v", err)
	}
	unknown, _ := NewFeature("unknown-feature")
	if _, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-5", Feature: unknown, Quantity: 1, UsageKey: "u4", At: testNow,
	}); !errors.Is(err, ErrFeatureNotInTariff) {
		t.Fatalf("возможность вне тарифа: ошибка = %v", err)
	}
}

func TestSubscriptionIssueInvoiceWithinLimits(t *testing.T) {
	subscription := newTestSubscription(t, 10)
	in := InvoicingInput{Period: subscription.Period(), At: testNow, EventID: "evt-1"}

	invoice, events, err := subscription.IssueInvoice(in)
	if err != nil {
		t.Fatalf("IssueInvoice: %v", err)
	}
	if len(events) != 1 || events[0].Name() != EventInvoiceIssued {
		t.Fatalf("событие выставления = %+v", events)
	}
	if invoice.Amount().AmountMinor() != 500000 {
		t.Fatalf("сумма счёта = %d", invoice.Amount().AmountMinor())
	}

	repeated, events, err := subscription.IssueInvoice(in)
	if err != nil {
		t.Fatalf("повтор IssueInvoice: %v", err)
	}
	if repeated.ID() != invoice.ID() || len(events) != 0 {
		t.Fatalf("повтор должен быть идемпотентен: id=%s events=%d", repeated.ID(), len(events))
	}
}

func TestSubscriptionSuspendResume(t *testing.T) {
	subscription := newTestSubscription(t, 10)
	email, _ := NewFeature("outreach-channel-email")

	if err := subscription.Suspend(testNow); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if err := subscription.Suspend(testNow); !errors.Is(err, ErrStatusUnchanged) {
		t.Fatalf("повторная приостановка: ошибка = %v", err)
	}
	if _, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-1", Feature: email, Quantity: 1, UsageKey: "u1", At: testNow,
	}); !errors.Is(err, ErrSubscriptionNotActive) {
		t.Fatalf("потребление приостановленной подписки: ошибка = %v", err)
	}
	if err := subscription.Resume(testNow); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-1", Feature: email, Quantity: 1, UsageKey: "u1", At: testNow,
	}); err != nil {
		t.Fatalf("потребление после возобновления: %v", err)
	}
}

func TestSubscriptionSnapshotRoundTrip(t *testing.T) {
	subscription := newTestSubscription(t, 10)
	email, _ := NewFeature("outreach-channel-email")
	if _, err := subscription.RecordUsage(UsageInput{
		UsageID: "usage-1", Feature: email, Quantity: 2, UsageKey: "u1", At: testNow,
	}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	invoice, _, err := subscription.IssueInvoice(InvoicingInput{Period: subscription.Period(), At: testNow, EventID: "evt-1"})
	if err != nil {
		t.Fatalf("IssueInvoice: %v", err)
	}
	if _, err := subscription.MarkInvoicePaid(invoice.ID(), testNow.Add(time.Hour), "pay-1"); err != nil {
		t.Fatalf("MarkInvoicePaid: %v", err)
	}

	snapshot := subscription.Snapshot()
	restored, err := RehydrateSubscription(snapshot)
	if err != nil {
		t.Fatalf("RehydrateSubscription: %v", err)
	}
	if restored.Version() != subscription.Version() || len(restored.Usage()) != 1 || len(restored.Invoices()) != 1 {
		t.Fatalf("восстановление не совпало: %+v", restored)
	}
	if !restored.Invoices()[0].Paid() {
		t.Fatal("отметка об оплате потеряна")
	}

	tampered := snapshot
	tampered.Limits = append([]TariffLimit(nil), snapshot.Limits...)
	tampered.Limits[0] = tampered.Limits[0].withUsage(5)
	if _, err := RehydrateSubscription(tampered); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("рассогласованное потребление: ошибка = %v", err)
	}
}
