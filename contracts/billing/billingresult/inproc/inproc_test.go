package inproc_test

import (
	"context"
	"testing"
	"time"

	"lead-gen/contracts/billing/billingresult"
	"lead-gen/contracts/billing/billingresult/inproc"
)

type recordingSubscriber struct {
	received []billingresult.Result
}

func (s *recordingSubscriber) Receive(_ context.Context, result billingresult.Result) error {
	s.received = append(s.received, result)
	return nil
}

func result() billingresult.Result {
	return billingresult.Result{
		Envelope: billingresult.Envelope{
			EventID:       "evt-1",
			OccurredAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          billingresult.ContractName,
			SchemaVersion: billingresult.SchemaVersion,
		},
		ResultID:       "res-1",
		Kind:           billingresult.KindCharge,
		SubscriptionID: "sub-1",
		SourceRef:      "invoice-1",
		AmountMinor:    500000,
		Currency:       "RUB",
	}
}

// TestAdapterPublishesValidatedResult проверяет прямую доставку результата через
// адаптер.
func TestAdapterPublishesValidatedResult(t *testing.T) {
	subscriber := &recordingSubscriber{}
	adapter, err := inproc.Bind(subscriber)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := adapter.Publish(context.Background(), result()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(subscriber.received) != 1 {
		t.Fatalf("получатель получил %d результатов", len(subscriber.received))
	}
}

// TestAdapterRejectsInvalidResult проверяет, что невалидный результат не доходит
// до получателя.
func TestAdapterRejectsInvalidResult(t *testing.T) {
	subscriber := &recordingSubscriber{}
	adapter, _ := inproc.Bind(subscriber)
	invalid := result()
	invalid.AmountMinor = -1
	if err := adapter.Publish(context.Background(), invalid); err == nil {
		t.Fatal("невалидный результат должен отвергаться адаптером")
	}
	if len(subscriber.received) != 0 {
		t.Fatalf("получатель не должен получать результат: %d", len(subscriber.received))
	}
}
