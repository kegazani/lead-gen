package usagerecord

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testInstant = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func validRecord() UsageRecord {
	return UsageRecord{
		Envelope: Envelope{
			EventID:       "evt-1",
			OccurredAt:    testInstant,
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          ContractName,
			SchemaVersion: SchemaVersion,
		},
		RecordID:  "rec-1",
		Feature:   "outreach-channel-email",
		Quantity:  1,
		PeriodKey: "period-1",
		UsageKey:  "usage-1",
	}
}

func TestUsageRecordValidate(t *testing.T) {
	if err := validRecord().Validate(); err != nil {
		t.Fatalf("валидная запись отвергнута: %v", err)
	}
	err := (UsageRecord{Envelope: Envelope{EventID: "e", OccurredAt: testInstant, TenantID: "t", CorrelationID: "c", Name: ContractName, SchemaVersion: SchemaVersion}}).Validate()
	if err == nil {
		t.Fatal("пустая запись должна быть невалидна")
	}
	for _, field := range []string{"record_id", "feature", "quantity", "period_key", "usage_key"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

func TestUsageRecordIdempotencyKey(t *testing.T) {
	a := validRecord()
	b := a
	b.EventID = "evt-2"
	b.RecordID = "rec-2"
	if a.IdempotencyKey() != b.IdempotencyKey() {
		t.Fatal("повтор той же записи должен давать тот же ключ")
	}
	c := a
	c.PeriodKey = "period-2"
	if c.IdempotencyKey() == a.IdempotencyKey() {
		t.Fatal("другой период должен менять ключ")
	}
	// Разделитель не допускает коллизий.
	x := a
	x.PeriodKey, x.UsageKey = "p|1", "2"
	y := a
	y.PeriodKey, y.UsageKey = "p", "1|2"
	if x.IdempotencyKey() == y.IdempotencyKey() {
		t.Fatalf("ключи совпали: %q", x.IdempotencyKey())
	}
}

func TestInProcWithoutHandlerIsUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Record(t.Context(), validRecord()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
