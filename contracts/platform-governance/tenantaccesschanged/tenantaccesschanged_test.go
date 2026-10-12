package tenantaccesschanged

import (
	"strings"
	"testing"
	"time"
)

func TestAccessChangeValidate(t *testing.T) {
	valid := AccessChange{
		Envelope: Envelope{EventID: "event-1", OccurredAt: time.Now(), TenantID: "tenant-1",
			CorrelationID: "corr-1", Name: ContractName, SchemaVersion: SchemaVersion},
		Change: "role-assigned", UserID: "user-1", Roles: []string{"operator"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid published event: %v", err)
	}
	invalid := valid
	invalid.EventID = ""
	invalid.Change = ""
	err := invalid.Validate()
	if err == nil || !strings.Contains(err.Error(), "event_id") || !strings.Contains(err.Error(), "change") {
		t.Fatalf("Validate error = %v, expected required-field diagnostics", err)
	}
}
