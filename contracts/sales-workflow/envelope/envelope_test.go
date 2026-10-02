package envelope_test

import (
	"testing"
	"time"

	"lead-gen/contracts/sales-workflow/envelope"
)

func valid() envelope.Envelope {
	return envelope.Envelope{
		EventID:       "evt-1",
		OccurredAt:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		Name:          "deal-state",
		SchemaVersion: "1.0.0",
	}
}

func TestEnvelopeValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("корректный конверт: %v", err)
	}

	cases := map[string]func(*envelope.Envelope){
		"без event_id":       func(e *envelope.Envelope) { e.EventID = "" },
		"без occurred_at":    func(e *envelope.Envelope) { e.OccurredAt = time.Time{} },
		"без tenant_id":      func(e *envelope.Envelope) { e.TenantID = "" },
		"без correlation_id": func(e *envelope.Envelope) { e.CorrelationID = "" },
		"без name":           func(e *envelope.Envelope) { e.Name = "" },
		"битая версия":       func(e *envelope.Envelope) { e.SchemaVersion = "1.0" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := valid()
			mutate(&env)
			if err := env.Validate(); err == nil {
				t.Fatal("некорректный конверт не отвергнут")
			}
		})
	}
}

func TestIsValidSemver(t *testing.T) {
	for _, version := range []string{"1.0.0", "0.1.2", "2.3.4-rc.1", "1.0.0+build.5"} {
		if !envelope.IsValidSemver(version) {
			t.Fatalf("%q признана невалидной", version)
		}
	}
	for _, version := range []string{"", "1.0", "v1.0.0", "1.0.0.0"} {
		if envelope.IsValidSemver(version) {
			t.Fatalf("%q признана валидной", version)
		}
	}
}

func TestEncodeKeyPartAvoidsCollisions(t *testing.T) {
	if envelope.EncodeKeyPart("a|b") == envelope.EncodeKeyPart("a")+"|"+envelope.EncodeKeyPart("b") {
		t.Fatal("разделитель не экранируется")
	}
}
