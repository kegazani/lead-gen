package accessdecision_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/contracts/platform-governance/accessdecision"
)

func validSnapshot() accessdecision.RuleSnapshot {
	return accessdecision.RuleSnapshot{
		Envelope: accessdecision.Envelope{
			EventID:       "evt-1",
			OccurredAt:    time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC),
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          accessdecision.ContractName,
			SchemaVersion: accessdecision.SchemaVersion,
		},
		State:           accessdecision.StateActive,
		SnapshotVersion: 3,
		Assignments: []accessdecision.Assignment{
			{Actor: "operator-1", Roles: []string{"platform-admin"}},
		},
	}
}

func TestRuleSnapshotValidateAcceptsValid(t *testing.T) {
	t.Parallel()
	if err := validSnapshot().Validate(); err != nil {
		t.Fatalf("корректный снимок отвергнут: %v", err)
	}
}

func TestRuleSnapshotValidateAcceptsEveryDomainTenantState(t *testing.T) {
	t.Parallel()
	for _, state := range []string{accessdecision.StateActive, accessdecision.StateDisabled, accessdecision.StatePending, accessdecision.StateRejected} {
		t.Run(state, func(t *testing.T) {
			snapshot := validSnapshot()
			snapshot.State = state
			if err := snapshot.Validate(); err != nil {
				t.Fatalf("domain state %q rejected: %v", state, err)
			}
		})
	}
}

func TestRuleSnapshotValidateCollectsAllDefects(t *testing.T) {
	t.Parallel()
	snapshot := validSnapshot()
	snapshot.EventID = ""
	snapshot.State = "unknown"
	snapshot.SnapshotVersion = 0
	snapshot.Assignments = []accessdecision.Assignment{{Actor: "", Roles: nil}}

	err := snapshot.Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка валидации")
	}
	for _, fragment := range []string{"event_id", "state", "snapshot_version", "assignments[0].actor", "assignments[0].roles"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("ошибка не называет поле %q: %v", fragment, err)
		}
	}
}

func TestRuleSnapshotIdempotencyKeyIncludesTenantAndVersion(t *testing.T) {
	t.Parallel()
	first := validSnapshot()
	first.TenantID = "tenant|1"
	second := validSnapshot()
	second.TenantID = "tenant"
	second.SnapshotVersion = 12

	if first.IdempotencyKey() == second.IdempotencyKey() {
		t.Fatal("ключ идемпотентности не различает тенанта и версию")
	}
	if !strings.HasPrefix(first.IdempotencyKey(), "tenant%7C1|") {
		t.Fatalf("ключ не кодирует разделитель в tenant_id: %q", first.IdempotencyKey())
	}
}

func TestUnavailableIsNotAllow(t *testing.T) {
	t.Parallel()
	allowed, err := accessdecision.Unavailable{}.CanAct(context.Background(), "operator-1", "tenant-1")
	if allowed {
		t.Fatal("недоступность владельца не должна давать разрешение")
	}
	if !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}
