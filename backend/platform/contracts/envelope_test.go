package contracts

import (
	"strings"
	"testing"
	"time"
)

func validEnvelope() Envelope {
	return Envelope{
		EventID:       "evt-1",
		OccurredAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		Name:          "approved-action",
		SchemaVersion: "1.2.0",
	}
}

func TestEnvelopeValidateAcceptsValid(t *testing.T) {
	if err := validEnvelope().Validate(); err != nil {
		t.Fatalf("валидный конверт отвергнут: %v", err)
	}
}

func TestEnvelopeValidateAllowsEmptyCausationID(t *testing.T) {
	e := validEnvelope()
	e.CausationID = ""
	if err := e.Validate(); err != nil {
		t.Fatalf("пустой causation_id у корня должен допускаться: %v", err)
	}
}

func TestEnvelopeValidateCollectsAllMissingFields(t *testing.T) {
	err := Envelope{SchemaVersion: "not-a-version"}.Validate()
	if err == nil {
		t.Fatal("пустой конверт должен быть отвергнут")
	}
	msg := err.Error()
	for _, field := range []string{"event_id", "occurred_at", "tenant_id", "correlation_id", "name", "schema_version"} {
		if !strings.Contains(msg, field) {
			t.Errorf("ошибка не упоминает поле %q: %s", field, msg)
		}
	}
}
