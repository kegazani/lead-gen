package domain

import (
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newTestTariff собирает тариф с двумя возможностями для тестов.
func newTestTariff(t *testing.T, emailLimit, enrichLimit int64) Tariff {
	t.Helper()
	price, err := NewMoney(500000, "rub")
	if err != nil {
		t.Fatalf("цена тарифа: %v", err)
	}
	email, err := NewTariffLimit("outreach-channel-email", emailLimit)
	if err != nil {
		t.Fatalf("лимит email: %v", err)
	}
	enrich, err := NewTariffLimit("osint-enrichment", enrichLimit)
	if err != nil {
		t.Fatalf("лимит обогащения: %v", err)
	}
	tariff, err := NewTariff("tariff-pro", price, []TariffLimit{email, enrich})
	if err != nil {
		t.Fatalf("тариф: %v", err)
	}
	return tariff
}

func TestMoneyArithmetic(t *testing.T) {
	a, err := NewMoney(1000, "rub")
	if err != nil {
		t.Fatalf("NewMoney: %v", err)
	}
	if got := a.Currency(); got != "RUB" {
		t.Fatalf("валюта нормализуется в верхний регистр: %q", got)
	}
	b, _ := NewMoney(250, "RUB")
	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if sum.AmountMinor() != 1250 {
		t.Fatalf("сумма = %d, ожидалось 1250", sum.AmountMinor())
	}
	diff, err := a.Subtract(b)
	if err != nil {
		t.Fatalf("Subtract: %v", err)
	}
	if diff.AmountMinor() != 750 {
		t.Fatalf("разность = %d, ожидалось 750", diff.AmountMinor())
	}
	if _, err := b.Subtract(a); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("отрицательная разность: ошибка = %v", err)
	}
	other, _ := NewMoney(1, "usd")
	if _, err := a.Add(other); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("разные валюты: ошибка = %v", err)
	}
}

func TestMoneyValidation(t *testing.T) {
	if _, err := NewMoney(-1, "rub"); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("отрицательная сумма: ошибка = %v", err)
	}
	if _, err := NewMoney(1, "ruble"); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("некорректная валюта: ошибка = %v", err)
	}
}

func TestMoneyMultiplyByBps(t *testing.T) {
	base, _ := NewMoney(100000, "rub")  // 1000.00
	fee, err := base.MultiplyByBps(500) // 5 %
	if err != nil {
		t.Fatalf("MultiplyByBps: %v", err)
	}
	if fee.AmountMinor() != 5000 {
		t.Fatalf("вознаграждение = %d, ожидалось 5000", fee.AmountMinor())
	}
	// Округление половины вверх.
	odd, _ := NewMoney(101, "rub")
	rounded, err := odd.MultiplyByBps(500)
	if err != nil {
		t.Fatalf("MultiplyByBps: %v", err)
	}
	if rounded.AmountMinor() != 5 {
		t.Fatalf("округление = %d, ожидалось 5", rounded.AmountMinor())
	}
	if _, err := base.MultiplyByBps(-1); !errors.Is(err, ErrInvalidRate) {
		t.Fatalf("отрицательная ставка: ошибка = %v", err)
	}
}

func TestBillingPeriod(t *testing.T) {
	start := testNow
	end := start.Add(24 * time.Hour)
	period, err := NewBillingPeriod(start, end)
	if err != nil {
		t.Fatalf("NewBillingPeriod: %v", err)
	}
	if !period.Contains(start) {
		t.Fatal("начало входит в период")
	}
	if period.Contains(end) {
		t.Fatal("конец не входит в полуинтервал")
	}
	if period.Key() == "" {
		t.Fatal("ключ периода не пуст")
	}
	if _, err := NewBillingPeriod(end, start); !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("обратный период: ошибка = %v", err)
	}
	if _, err := NewBillingPeriod(time.Time{}, end); !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("пустая граница: ошибка = %v", err)
	}
}

func TestTariffLimit(t *testing.T) {
	feature, _ := NewFeature("outreach-channel-email")
	limit, err := NewTariffLimit(feature, 100)
	if err != nil {
		t.Fatalf("NewTariffLimit: %v", err)
	}
	if limit.Exceeds(100) {
		t.Fatal("ровно лимит 100 не является превышением")
	}
	if !limit.Exceeds(101) {
		t.Fatal("101 при лимите 100 — превышение")
	}
	if limit.IsApproaching(90) {
		t.Fatal("нулевое потребление далеко от лимита")
	}
	near := limit.withUsage(95)
	if !near.IsApproaching(90) {
		t.Fatal("95 % потребления — приближение к лимиту")
	}
	if _, err := NewTariffLimit("", 1); !errors.Is(err, ErrUnknownFeature) {
		t.Fatalf("пустая возможность: ошибка = %v", err)
	}
	if _, err := NewTariffLimit(feature, -1); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("отрицательный лимит: ошибка = %v", err)
	}
}

func TestSubscriptionEntitlementPolicy(t *testing.T) {
	tariff := newTestTariff(t, 10, 100)
	feature, _ := NewFeature("outreach-channel-email")

	allowed, err := SubscriptionEntitlement(tariff, feature, 3, 0, false)
	if err != nil {
		t.Fatalf("SubscriptionEntitlement: %v", err)
	}
	if !allowed.Allowed || !allowed.FeatureEntitled || allowed.Remaining != 7 {
		t.Fatalf("решение = %+v", allowed)
	}

	exceeded, err := SubscriptionEntitlement(tariff, feature, 5, 8, false)
	if err != nil {
		t.Fatalf("SubscriptionEntitlement: %v", err)
	}
	if exceeded.Allowed {
		t.Fatalf("превышение лимита должно отвергаться: %+v", exceeded)
	}

	unknown, _ := NewFeature("unknown-feature")
	notEntitled, err := SubscriptionEntitlement(tariff, unknown, 1, 0, false)
	if err != nil {
		t.Fatalf("SubscriptionEntitlement: %v", err)
	}
	if notEntitled.FeatureEntitled || notEntitled.Allowed {
		t.Fatalf("возможность вне тарифа: %+v", notEntitled)
	}

	exhausting, err := SubscriptionEntitlement(tariff, feature, 3, 7, false)
	if err != nil {
		t.Fatalf("SubscriptionEntitlement: %v", err)
	}
	if !exhausting.ShouldIssueInvoice {
		t.Fatalf("исчерпание периода должно требовать счёт: %+v", exhausting)
	}
	invoiced, _ := SubscriptionEntitlement(tariff, feature, 3, 7, true)
	if invoiced.ShouldIssueInvoice {
		t.Fatalf("выставленный счёт не выставляется повторно: %+v", invoiced)
	}
}

func TestSuccessFeeEligibilityPolicy(t *testing.T) {
	base, _ := NewMoney(100000, "rub")
	result := AttributedResult{
		ResultRef:             "result-1",
		TenantID:              "tenant-1",
		DealID:                "deal-1",
		Confirmed:             true,
		ClosedInPlatformCRM:   true,
		CommunicationRecorded: true,
		Amount:                base,
	}

	decision, err := SuccessFeeEligibility(result)
	if err != nil {
		t.Fatalf("SuccessFeeEligibility: %v", err)
	}
	if !decision.Eligible || decision.RateBps != 500 || decision.Calculation != CurrentCalculationVersion {
		t.Fatalf("решение = %+v", decision)
	}

	cases := []struct {
		name   string
		mutate func(*AttributedResult)
	}{
		{"не подтверждён", func(r *AttributedResult) { r.Confirmed = false }},
		{"вне CRM платформы", func(r *AttributedResult) { r.ClosedInPlatformCRM = false }},
		{"коммуникация не подтверждена", func(r *AttributedResult) { r.CommunicationRecorded = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modified := result
			tc.mutate(&modified)
			decision, err := SuccessFeeEligibility(modified)
			if err != nil {
				t.Fatalf("SuccessFeeEligibility: %v", err)
			}
			if decision.Eligible {
				t.Fatalf("случай %q не должен быть кандидатом: %+v", tc.name, decision)
			}
			if decision.Reason == "" {
				t.Fatal("решение об исключении обязано называть основание")
			}
		})
	}

	invalid := result
	invalid.ResultRef = ""
	if _, err := SuccessFeeEligibility(invalid); !errors.Is(err, ErrResultRefRequired) {
		t.Fatalf("пустая ссылка на результат: ошибка = %v", err)
	}
}

func TestRateFor(t *testing.T) {
	if _, ok := RateFor(CurrentCalculationVersion); !ok {
		t.Fatal("текущая версия правила обязана иметь ставку")
	}
	if _, ok := RateFor("v-unknown"); ok {
		t.Fatal("неизвестная версия правила не имеет ставки")
	}
}
