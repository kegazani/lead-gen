package inproc_test

import (
	"context"
	"testing"
	"time"

	"lead-gen/contracts/billing/usagerecord"
	"lead-gen/contracts/billing/usagerecord/inproc"
)

type countingRecorder struct {
	calls int
}

func (r *countingRecorder) Record(context.Context, usagerecord.UsageRecord) (usagerecord.Ack, error) {
	r.calls++
	return usagerecord.Ack{Accepted: true, Remaining: 5}, nil
}

func record(usageKey string) usagerecord.UsageRecord {
	return usagerecord.UsageRecord{
		Envelope: usagerecord.Envelope{
			EventID:       "evt-1",
			OccurredAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          usagerecord.ContractName,
			SchemaVersion: usagerecord.SchemaVersion,
		},
		RecordID:  "rec-1",
		Feature:   "outreach-channel-email",
		Quantity:  1,
		PeriodKey: "period-1",
		UsageKey:  usageKey,
	}
}

// TestAdapterDeduplicatesByKey проверяет, что повтор записи не доходит до
// владельца второй раз.
func TestAdapterDeduplicatesByKey(t *testing.T) {
	owner := &countingRecorder{}
	adapter, err := inproc.Bind(owner)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	ctx := context.Background()

	first, err := adapter.Record(ctx, record("usage-1"))
	if err != nil || !first.Accepted {
		t.Fatalf("первый вызов: ack=%+v err=%v", first, err)
	}
	second, err := adapter.Record(ctx, record("usage-1"))
	if err != nil || !second.Duplicate {
		t.Fatalf("повтор: ack=%+v err=%v", second, err)
	}
	if owner.calls != 1 {
		t.Fatalf("владелец вызван %d раз, ожидалось 1", owner.calls)
	}
}

// TestAdapterValidatesBeforeCall проверяет, что невалидная запись не доходит до
// владельца.
func TestAdapterValidatesBeforeCall(t *testing.T) {
	owner := &countingRecorder{}
	adapter, _ := inproc.Bind(owner)
	invalid := record("usage-1")
	invalid.Feature = ""
	if _, err := adapter.Record(context.Background(), invalid); err == nil {
		t.Fatal("невалидная запись должна отвергаться адаптером")
	}
	if owner.calls != 0 {
		t.Fatalf("владелец не должен вызываться: %d", owner.calls)
	}
}
