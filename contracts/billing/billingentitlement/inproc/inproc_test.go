package inproc_test

import (
	"context"
	"testing"
	"time"

	"lead-gen/contracts/billing/billingentitlement"
	"lead-gen/contracts/billing/billingentitlement/inproc"
)

type stubSource struct {
	entitlement billingentitlement.Entitlement
}

func (s stubSource) Entitlement(context.Context, string) (billingentitlement.Entitlement, error) {
	return s.entitlement, nil
}

func source() billingentitlement.Entitlement {
	return billingentitlement.Entitlement{
		Envelope: billingentitlement.Envelope{
			EventID:       "evt-1",
			OccurredAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          billingentitlement.ContractName,
			SchemaVersion: billingentitlement.SchemaVersion,
		},
		SubscriptionID: "sub-1",
		TariffID:       "tariff-pro",
		Status:         "active",
		PeriodStart:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:      time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

// TestAdapterReadsEntitlement проверяет прямой вызов владельца через адаптер.
func TestAdapterReadsEntitlement(t *testing.T) {
	adapter, err := inproc.Bind(stubSource{entitlement: source()})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	entitlement, err := adapter.Entitlement(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Entitlement: %v", err)
	}
	if entitlement.SubscriptionID != "sub-1" {
		t.Fatalf("права = %+v", entitlement)
	}
}

// TestAdapterRejectsInvalidEntitlement проверяет, что невалидные права не
// выдаются потребителю как валидные.
func TestAdapterRejectsInvalidEntitlement(t *testing.T) {
	broken := source()
	broken.SubscriptionID = ""
	adapter, _ := inproc.Bind(stubSource{entitlement: broken})
	if _, err := adapter.Entitlement(context.Background(), "tenant-1"); err == nil {
		t.Fatal("невалидные права должны отвергаться адаптером")
	}
}
