package postgres

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWireEnvelopeAuditRecordedUsesStableContractFields(t *testing.T) {
	payload := []byte(`{"EventHeader":{"EventID":"audit:1","OccurredAt":"2026-01-02T03:04:05Z","TenantID":"tenant-1","CorrelationID":"corr-1","CausationID":"cmd-1"},"AuditRecordID":"audit-1","Actor":"operator-1","Action":"tenant.disabled","Object":"tenant:tenant-1","Outcome":"success","CorrectionOf":""}`)
	body, err := wireEnvelope("audit-recorded", "1.0.0", "audit:1", "tenant-1", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "corr-1", "cmd-1", payload)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"event_id", "occurred_at", "tenant_id", "correlation_id", "causation_id", "name", "schema_version", "audit_record_id", "actor", "action", "object", "outcome"} {
		if _, ok := envelope[key]; !ok {
			t.Errorf("wire contract missing %q", key)
		}
	}
	if envelope["event_id"] != "audit:1" || envelope["actor"] != "operator-1" {
		t.Fatalf("wire contract changed stable identifiers: %#v", envelope)
	}
}

func TestWireEnvelopeFillsMissingOptionalCorrelationFromOutbox(t *testing.T) {
	payload := []byte(`{"EventHeader":{"EventID":"audit:2","OccurredAt":"2026-01-02T03:04:05Z","TenantID":"tenant-1","CorrelationID":""},"AuditRecordID":"audit-2","Actor":"operator-1","Action":"tenant.disabled","Object":"tenant:tenant-1","Outcome":"success"}`)
	body, err := wireEnvelope("audit-recorded", "1.0.0", "audit:2", "tenant-1", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "audit:2", "", payload)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["correlation_id"] != "audit:2" {
		t.Fatalf("wire correlation_id = %#v, expected outbox fallback", envelope["correlation_id"])
	}
}

func TestWireEnvelopeAccessDecisionFlattensSnapshotAndNoSecretFields(t *testing.T) {
	payload := []byte(`{"TenantID":"tenant-1","State":"active","Version":4,"Assignments":[{"Actor":"actor-1","Roles":["platform-admin"]}],"OccurredAt":"2026-01-02T03:04:05Z"}`)
	body, err := wireEnvelope("access-decision", "1.0.0", "evt-1", "tenant-1", time.Now().UTC(), "corr-1", "", payload)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["snapshot_version"] != float64(4) {
		t.Fatalf("snapshot_version missing: %#v", envelope)
	}
	if strings.Contains(string(body), "password") || strings.Contains(string(body), "secret") || strings.Contains(string(body), "login") {
		t.Fatalf("wire payload contains prohibited field: %s", body)
	}
}

func TestWireEnvelopeRejectsInvalidPayloadWithoutEchoingData(t *testing.T) {
	secret := `{"token":"sensitive-value"}`
	_, err := wireEnvelope("audit-recorded", "1.0.0", "evt", "tenant", time.Now(), "corr", "", []byte(secret))
	if err == nil || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatalf("invalid payload was accepted or echoed: %v", err)
	}
}
