package tenantcontext

import "testing"

func TestContextValidate(t *testing.T) {
	if err := (Context{TenantID: "tenant-1"}).Validate(); err != nil {
		t.Fatalf("valid tenant context rejected: %v", err)
	}
	if err := (Context{}).Validate(); err == nil {
		t.Fatal("empty tenant id accepted")
	}
}
