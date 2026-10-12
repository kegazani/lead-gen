package auditrecorded

import (
	"strings"
	"testing"
	"time"
)

func TestAuditRecordValidate(t *testing.T) {
	event := AuditRecord{Envelope: Envelope{
		EventID: "evt-1", OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		TenantID: "tenant-1", CorrelationID: "corr-1", Name: ContractName, SchemaVersion: SchemaVersion,
	}, AuditRecordID: "audit-1", Actor: "actor-1", Action: "tenant.disabled", Object: "tenant:tenant-1", Outcome: "success"}
	if err := event.Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	event.Actor = ""
	err := event.Validate()
	if err == nil || !strings.Contains(err.Error(), "actor") {
		t.Fatalf("missing actor not reported: %v", err)
	}
}
