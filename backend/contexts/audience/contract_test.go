package audience_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/audience"
	"lead-gen/backend/contexts/audience/internal/app/command"
	"lead-gen/backend/contexts/audience/internal/ports/contracts"
	"lead-gen/contracts/audience/audiencesnapshot"
	"lead-gen/contracts/audience/segmentmembership"
)

var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// buildSlice собирает срез и создаёт сегмент с одним лидом в составе.
func buildSlice(t *testing.T) *audience.Slice {
	t.Helper()
	slice, err := audience.Build(audience.Params{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	if _, err := slice.CreateSegment.Handle(context.Background(), command.CreateSegment{
		SegmentID: "segment-1", TenantID: "tenant-1", Name: "Тёплые лиды", ModelVersion: "v1", MinScore: 0.5,
	}); err != nil {
		t.Fatalf("создание сегмента: %v", err)
	}
	if _, err := slice.ScoreLead.Handle(context.Background(), command.ScoreLead{
		SegmentID: "segment-1",
		TenantID:  "tenant-1",
		Profile: contracts.LeadProfile{
			LeadID: "lead-1", TenantID: "tenant-1", ObservedAt: epoch,
			Signals: []contracts.SignalValue{{Name: "fit", Strength: 0.9}},
		},
		Actor: "actor-1",
	}); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	return slice
}

// TestAudienceSnapshotContract — in-proc адаптер контракта audience-snapshot
// отдаёт снимок владельца, а отсутствующий снимок — ErrNotFound.
func TestAudienceSnapshotContract(t *testing.T) {
	slice := buildSlice(t)
	fixed, err := slice.FixSnapshot.Handle(context.Background(), command.FixAudienceSnapshot{
		SegmentID: "segment-1", TenantID: "tenant-1", Actor: "actor-1",
	})
	if err != nil {
		t.Fatalf("фиксация снимка: %v", err)
	}

	port := audience.NewAudienceSnapshot(slice)
	env := audiencesnapshot.Envelope{
		EventID: "evt-1", OccurredAt: epoch, TenantID: "tenant-1", CorrelationID: "corr-1",
	}
	snapshot, err := port.Fetch(context.Background(), env, fixed.SnapshotID.String())
	if err != nil {
		t.Fatalf("запрос снимка: %v", err)
	}
	if snapshot.Name != audiencesnapshot.ContractName || snapshot.SchemaVersion != audiencesnapshot.SchemaVersion {
		t.Fatalf("конверт контракта: %+v", snapshot.Envelope)
	}
	if snapshot.MemberCount != 1 || snapshot.SegmentID != "segment-1" {
		t.Fatalf("снимок: %+v", snapshot)
	}

	if _, err := port.Fetch(context.Background(), env, "missing"); !errors.Is(err, audiencesnapshot.ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestSegmentMembershipContract — in-proc адаптер контракта segment-membership
// отдаёт состав сегмента.
func TestSegmentMembershipContract(t *testing.T) {
	slice := buildSlice(t)
	port := audience.NewSegmentMembership(slice)
	env := segmentmembership.Envelope{
		EventID: "evt-1", OccurredAt: epoch, TenantID: "tenant-1", CorrelationID: "corr-1",
	}
	membership, err := port.Membership(context.Background(), env, "segment-1")
	if err != nil {
		t.Fatalf("запрос состава: %v", err)
	}
	if membership.MemberCount != 1 || len(membership.Members) != 1 || membership.Members[0] != "lead-1" {
		t.Fatalf("состав: %+v", membership)
	}
	if membership.ChangedAt.IsZero() {
		t.Fatal("момент изменения состава должен быть заполнен")
	}

	if _, err := port.Membership(context.Background(), env, "segment-missing"); !errors.Is(err, segmentmembership.ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}
