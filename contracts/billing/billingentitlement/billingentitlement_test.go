package billingentitlement

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testInstant = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func validEntitlement() Entitlement {
	return Entitlement{
		Envelope: Envelope{
			EventID:       "evt-1",
			OccurredAt:    testInstant,
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          ContractName,
			SchemaVersion: SchemaVersion,
		},
		SubscriptionID: "sub-1",
		TariffID:       "tariff-pro",
		Status:         "active",
		PeriodStart:    testInstant,
		PeriodEnd:      testInstant.Add(24 * time.Hour),
		Limits: []Limit{
			{Feature: "outreach-channel-email", Limit: 10, Used: 3, Remaining: 7},
		},
	}
}

func TestEntitlementValidate(t *testing.T) {
	if err := validEntitlement().Validate(); err != nil {
		t.Fatalf("валидные права отвергнуты: %v", err)
	}
	broken := validEntitlement()
	broken.SubscriptionID = ""
	broken.PeriodEnd = time.Time{}
	err := broken.Validate()
	if err == nil {
		t.Fatal("неполные права должны быть невалидны")
	}
	for _, field := range []string{"subscription_id", "period_end"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

func TestEntitlementAllowsAndRemaining(t *testing.T) {
	entitlement := validEntitlement()
	if !entitlement.Allows("outreach-channel-email") {
		t.Fatal("возможность тарифа должна быть разрешена")
	}
	if entitlement.Allows("unknown-feature") {
		t.Fatal("возможность вне тарифа не должна быть разрешена")
	}
	if remaining, ok := entitlement.Remaining("outreach-channel-email"); !ok || remaining != 7 {
		t.Fatalf("остаток = %d, ok=%v", remaining, ok)
	}
}

func TestInProcWithoutHandlerIsUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Entitlement(t.Context(), "tenant-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
