package deliverabilitypolicy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testInstant = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func validPolicy() Policy {
	policy := NewPolicy(Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "act-1",
	})
	policy.PolicyID = "policy-1"
	policy.SenderDomain = "mail.example.com"
	policy.Scope = "domain:mail.example.com"
	policy.MaxAttempts = 3
	policy.EffectiveAt = testInstant
	return policy
}

func TestPolicy_RoundTrip(t *testing.T) {
	original := validPolicy()
	if err := original.Validate(); err != nil {
		t.Fatalf("исходный снимок должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored Policy
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, restored) {
		t.Fatalf("round-trip не совпал:\n%+v\n%+v", original, restored)
	}
}

func TestPolicy_RequiredFields(t *testing.T) {
	empty := NewPolicy(Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "act-1",
	})
	err := empty.Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка об обязательных полях")
	}
	for _, field := range []string{"policy_id", "sender_domain", "scope", "max_attempts", "effective_at"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

func TestPolicy_SuspendedRequiresConstraint(t *testing.T) {
	suspended := validPolicy()
	suspended.Suspended = true
	if err := suspended.Validate(); err == nil || !strings.Contains(err.Error(), "constraints") {
		t.Fatalf("приостановка без ограничения должна отвергаться, got %v", err)
	}

	suspended.Constraints = []Constraint{ConstraintPaused}
	if err := suspended.Validate(); err != nil {
		t.Fatalf("приостановка с ограничением должна быть валидна: %v", err)
	}

	unknown := validPolicy()
	unknown.Constraints = []Constraint{"nope"}
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "constraints") {
		t.Fatalf("неизвестное ограничение должно отвергаться, got %v", err)
	}
}

func TestPolicy_IdempotencyKey(t *testing.T) {
	policy := validPolicy()
	if got, want := policy.IdempotencyKey(), "tenant-1|mail.example.com|policy-1"; got != want {
		t.Fatalf("ключ: got %q, want %q", got, want)
	}
}
