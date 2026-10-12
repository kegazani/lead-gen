package domain

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestExecutionIntentCommandIdentityAndLegacyTarget(t *testing.T) {
	first, err := NewExecutionIntent("cmd-a", "req-a", "tenant-a", 1, LeadTarget{State: LeadTargetExplicit, Value: 0}, json.RawMessage(`{"limit":3,"query":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := NewExecutionIntent("cmd-a", "req-a", "tenant-a", 1, LeadTarget{State: LeadTargetExplicit, Value: 0}, json.RawMessage(`{"query":"x","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if !first.SameCommand(retry) {
		t.Fatal("повтор с тем же CommandID и канонически равными параметрами не распознан")
	}
	conflict, err := NewExecutionIntent("cmd-a", "req-a", "tenant-a", 1, LeadTarget{State: LeadTargetExplicit, Value: 7}, json.RawMessage(`{"limit":3,"query":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.SameCommand(conflict) {
		t.Fatal("изменённая LeadTarget прошла как тот же запрос")
	}

	legacy, err := RehydrateExecutionIntent(ExecutionIntentSnapshot{CommandID: "legacy", RequestID: "req-a", TenantID: "tenant-a", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := legacy.Parameters().LeadTarget; got.State != LeadTargetUnknownLegacy {
		t.Fatalf("legacy target = %+v, ожидался unknownLegacy", got)
	}
	missingExplicit, err := RehydrateExecutionIntent(ExecutionIntentSnapshot{CommandID: "legacy-explicit", RequestID: "req-a", TenantID: "tenant-a", Generation: 1, TargetState: LeadTargetExplicit})
	if err != nil {
		t.Fatal(err)
	}
	if got := missingExplicit.Parameters().LeadTarget; got.State != LeadTargetUnknownLegacy {
		t.Fatalf("missing explicit field = %+v, ожидался unknownLegacy", got)
	}
	if _, err := NewExecutionIntent("bad", "req-a", "tenant-a", 1, LeadTarget{State: LeadTargetUnknownLegacy, Value: 1}, nil); !errors.Is(err, ErrInvalidExecutionIntent) {
		t.Fatalf("невалидный target: %v", err)
	}
}
