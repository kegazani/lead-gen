package billing_test

import (
	"fmt"
	"testing"
	"time"

	billing "lead-gen/backend/contexts/billing"
	"lead-gen/backend/contexts/billing/internal/adapters/payment"
	"lead-gen/backend/contexts/billing/internal/domain"
)

var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fixedClock — детерминированные часы прикладного слоя.
type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time          { return c.now }
func (c *fixedClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// seqIDs — детерминированный генератор идентификаторов событий.
type seqIDs struct {
	n int64
}

func (g *seqIDs) NewEventID() domain.EventID {
	g.n++
	return domain.EventID(fmt.Sprintf("evt-%d", g.n))
}

// testTariff собирает тариф с двумя возможностями.
func testTariff(t *testing.T) domain.Tariff {
	t.Helper()
	price, err := domain.NewMoney(500000, "rub")
	if err != nil {
		t.Fatalf("цена тарифа: %v", err)
	}
	email, err := domain.NewTariffLimit("outreach-channel-email", 10)
	if err != nil {
		t.Fatalf("лимит email: %v", err)
	}
	enrichment, err := domain.NewTariffLimit("osint-enrichment", 100)
	if err != nil {
		t.Fatalf("лимит обогащения: %v", err)
	}
	tariff, err := domain.NewTariff("tariff-pro", price, []domain.TariffLimit{email, enrichment})
	if err != nil {
		t.Fatalf("тариф: %v", err)
	}
	return tariff
}

// testPeriod — тарифный период теста.
func testPeriod(t *testing.T) domain.BillingPeriod {
	t.Helper()
	period, err := domain.NewBillingPeriod(testNow, testNow.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("период: %v", err)
	}
	return period
}

// newSlice собирает срез billing с детерминированными часами, идентификаторами и
// платёжным адаптером без сети.
func newSlice(t *testing.T, clock *fixedClock) (*billing.Slice, *payment.Deterministic) {
	t.Helper()
	gateway := payment.NewDeterministic()
	slice, err := billing.Build(billing.Config{
		Clock:          clock,
		IDs:            &seqIDs{},
		PaymentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	return slice, gateway
}

// billingBuildWithoutPayment собирает срез без платёжной системы: порт честно
// отвечает «недоступно».
func billingBuildWithoutPayment(t *testing.T) (*billing.Slice, error) {
	t.Helper()
	return billing.Build(billing.Config{
		Clock: &fixedClock{now: testNow},
		IDs:   &seqIDs{},
	})
}
