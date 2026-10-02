package billing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/billing/internal/app/command"
	"lead-gen/backend/contexts/billing/internal/app/query"
	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// TestBillingSubscriptionFlow проходит сквозной сценарий подписки: активация,
// потребление до исчерпания лимита (с выставлением счёта), оплата счёта,
// приостановка и возобновление, расчёт и оспаривание вознаграждения и расчётный
// проход worker.
func TestBillingSubscriptionFlow(t *testing.T) {
	ctx := context.Background()
	clock := &fixedClock{now: testNow}
	slice, gateway := newSlice(t, clock)

	activated, err := slice.ActivateSubscription.Handle(ctx, command.ActivateSubscription{
		SubscriptionID: "sub-1",
		TenantID:       "tenant-1",
		Tariff:         testTariff(t),
		Period:         testPeriod(t),
	})
	if err != nil {
		t.Fatalf("активация: %v", err)
	}
	if activated.Status != domain.StatusActive {
		t.Fatalf("статус после активации = %s", activated.Status)
	}

	// Потребление ровно до предела: политика признаёт период исчерпанным и
	// выставляет счёт.
	usage, err := slice.RecordUsage.Handle(ctx, command.RecordUsage{
		SubscriptionID: "sub-1",
		TenantID:       "tenant-1",
		UsageID:        "usage-1",
		Feature:        "outreach-channel-email",
		Quantity:       10,
		UsageKey:       "u1",
	})
	if err != nil {
		t.Fatalf("фиксация потребления: %v", err)
	}
	if !usage.Recorded || usage.Remaining != 0 || !usage.InvoiceIssued || usage.InvoiceID == "" {
		t.Fatalf("результат потребления = %+v", usage)
	}

	// Повторная запись идемпотентна: лимит не расходуется дважды.
	duplicate, err := slice.RecordUsage.Handle(ctx, command.RecordUsage{
		SubscriptionID: "sub-1",
		TenantID:       "tenant-1",
		UsageID:        "usage-2",
		Feature:        "outreach-channel-email",
		Quantity:       10,
		UsageKey:       "u1",
	})
	if err != nil {
		t.Fatalf("повторная фиксация: %v", err)
	}
	if !duplicate.Duplicate {
		t.Fatalf("повтор должен быть дубликатом: %+v", duplicate)
	}

	// Превышение лимита отвергается.
	if _, err := slice.RecordUsage.Handle(ctx, command.RecordUsage{
		SubscriptionID: "sub-1",
		TenantID:       "tenant-1",
		UsageID:        "usage-3",
		Feature:        "outreach-channel-email",
		Quantity:       1,
		UsageKey:       "u2",
	}); !errors.Is(err, domain.ErrTariffLimitExceeded) {
		t.Fatalf("превышение лимита: ошибка = %v", err)
	}

	// Оплата счёта через детерминированный платёжный адаптер.
	charge, err := slice.ChargeInvoice.Handle(ctx, command.ChargeInvoice{
		SubscriptionID: "sub-1",
		TenantID:       "tenant-1",
		InvoiceID:      usage.InvoiceID,
	})
	if err != nil {
		t.Fatalf("оплата счёта: %v", err)
	}
	if !charge.Paid || gateway.Charged() != 1 {
		t.Fatalf("оплата = %+v, списаний %d", charge, gateway.Charged())
	}

	// Приостановка прерывает потребление; возобновление возвращает доступ.
	if _, err := slice.SuspendSubscription.Handle(ctx, command.SuspendSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("приостановка: %v", err)
	}
	if _, err := slice.RecordUsage.Handle(ctx, command.RecordUsage{
		SubscriptionID: "sub-1", TenantID: "tenant-1", UsageID: "usage-4",
		Feature: "osint-enrichment", Quantity: 1, UsageKey: "u3",
	}); !errors.Is(err, domain.ErrSubscriptionNotActive) {
		t.Fatalf("потребление приостановленной подписки: ошибка = %v", err)
	}
	if _, err := slice.ResumeSubscription.Handle(ctx, command.ResumeSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("возобновление: %v", err)
	}

	// Вознаграждение от подтверждённого результата.
	amount, _ := domain.NewMoney(200000, "rub")
	fee, err := slice.CalculateSuccessFee.Handle(ctx, command.CalculateSuccessFee{
		SuccessFeeID:          "fee-1",
		TenantID:              "tenant-1",
		SubscriptionID:        "sub-1",
		ResultRef:             "result-1",
		DealID:                "deal-1",
		Amount:                amount,
		Confirmed:             true,
		ClosedInPlatformCRM:   true,
		CommunicationRecorded: true,
	})
	if err != nil {
		t.Fatalf("расчёт вознаграждения: %v", err)
	}
	if !fee.Eligible || fee.Amount.AmountMinor() != 10000 {
		t.Fatalf("вознаграждение = %+v", fee)
	}

	// Неподтверждённый результат кандидатом не становится.
	ineligible, err := slice.CalculateSuccessFee.Handle(ctx, command.CalculateSuccessFee{
		SuccessFeeID: "fee-2", TenantID: "tenant-1", SubscriptionID: "sub-1",
		ResultRef: "result-2", DealID: "deal-2", Amount: amount,
		Confirmed: false, ClosedInPlatformCRM: true, CommunicationRecorded: true,
	})
	if err != nil {
		t.Fatalf("неподтверждённый результат: %v", err)
	}
	if ineligible.Eligible {
		t.Fatalf("неподтверждённый результат не должен быть кандидатом: %+v", ineligible)
	}

	// Спор приостанавливает списание.
	disputed, err := slice.DisputeSuccessFee.Handle(ctx, command.DisputeSuccessFee{
		SuccessFeeID: "fee-1", TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("оспаривание: %v", err)
	}
	if disputed.Status != domain.FeeDisputed {
		t.Fatalf("статус после спора = %s", disputed.Status)
	}

	view, err := slice.GetSubscription.Handle(ctx, query.GetSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("чтение подписки: %v", err)
	}
	if len(view.Usage) != 1 || len(view.Invoices) != 1 || !view.Invoices[0].Paid {
		t.Fatalf("представление подписки = %+v", view)
	}

	// Расчётный проход worker: текущий период завершился — открывается следующий,
	// а счёт за него выставляется следующим проходом.
	clock.advance(31 * 24 * time.Hour)
	settled, err := slice.SettleDuePeriods.Handle(ctx)
	if err != nil {
		t.Fatalf("расчётный проход: %v", err)
	}
	if settled.Renewed != 1 {
		t.Fatalf("период должен быть открыт заново: %+v", settled)
	}
	settled, err = slice.SettleDuePeriods.Handle(ctx)
	if err != nil {
		t.Fatalf("расчётный проход: %v", err)
	}
	if settled.InvoicesIssued != 1 {
		t.Fatalf("счёт за новый период должен быть выставлен: %+v", settled)
	}

	pending, err := slice.Journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("журнал outbox: %v", err)
	}
	if len(pending) < 4 {
		t.Fatalf("событий в outbox = %d, ожидалось не меньше 4", len(pending))
	}
}

// TestBillingTenantIsolation проверяет, что подписка чужого тенанта неотличима от
// отсутствующей.
func TestBillingTenantIsolation(t *testing.T) {
	ctx := context.Background()
	slice, _ := newSlice(t, &fixedClock{now: testNow})
	if _, err := slice.ActivateSubscription.Handle(ctx, command.ActivateSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-1", Tariff: testTariff(t), Period: testPeriod(t),
	}); err != nil {
		t.Fatalf("активация: %v", err)
	}
	_, err := slice.GetSubscription.Handle(ctx, query.GetSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-2",
	})
	if !errors.Is(err, ports.ErrSubscriptionNotFound) {
		t.Fatalf("чужой тенант: ошибка = %v", err)
	}
}

// TestBillingPaymentUnavailable проверяет, что без подключённой платёжной системы
// оплата честно отвечает «недоступно», а счёт остаётся неоплаченным.
func TestBillingPaymentUnavailable(t *testing.T) {
	ctx := context.Background()
	slice, err := billingBuildWithoutPayment(t)
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	if _, err := slice.ActivateSubscription.Handle(ctx, command.ActivateSubscription{
		SubscriptionID: "sub-1", TenantID: "tenant-1", Tariff: testTariff(t), Period: testPeriod(t),
	}); err != nil {
		t.Fatalf("активация: %v", err)
	}
	issued, err := slice.IssueInvoice.Handle(ctx, command.IssueInvoice{
		SubscriptionID: "sub-1", TenantID: "tenant-1", Period: testPeriod(t),
	})
	if err != nil {
		t.Fatalf("выставление счёта: %v", err)
	}
	if _, err := slice.ChargeInvoice.Handle(ctx, command.ChargeInvoice{
		SubscriptionID: "sub-1", TenantID: "tenant-1", InvoiceID: issued.InvoiceID,
	}); !errors.Is(err, ports.ErrPaymentUnavailable) {
		t.Fatalf("оплата без платёжной системы: ошибка = %v", err)
	}
}
