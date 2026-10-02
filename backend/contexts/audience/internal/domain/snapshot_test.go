package domain_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/audience/internal/domain"
)

// TestFixAudienceSnapshot — фиксация снимка копирует состав и версию сегмента и
// порождает audience-snapshot-created.
func TestFixAudienceSnapshot(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	if _, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(1)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	if _, err := segment.ScoreLead(profile(t, "lead-2", "tenant-1", signals(t, [2]any{"fit", 0.2})), provenance(2)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}

	snapshot, err := domain.FixAudienceSnapshot(segment, domain.NewAudienceSnapshotParams{
		ID:         "snap-1",
		TenantID:   "tenant-1",
		Provenance: provenance(3),
	})
	if err != nil {
		t.Fatalf("фиксация снимка: %v", err)
	}
	if snapshot.MemberCount() != 1 {
		t.Fatalf("размер снимка = %d, ожидался 1", snapshot.MemberCount())
	}
	if snapshot.SegmentVersion() != segment.Version() {
		t.Fatalf("версия сегмента = %d, ожидалась %d", snapshot.SegmentVersion(), segment.Version())
	}
	if !snapshot.Frozen() {
		t.Fatal("снимок обязан быть зафиксирован")
	}
	events := snapshot.DomainEvents()
	if len(events) != 1 || events[0].Name() != domain.EventAudienceSnapshotCreated {
		t.Fatalf("события = %v", events)
	}
}

// TestSnapshotImmutable — снимок хранит копию состава: последующее изменение
// сегмента не меняет уже зафиксированный снимок.
func TestSnapshotImmutable(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	if _, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(1)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	snapshot, err := domain.FixAudienceSnapshot(segment, domain.NewAudienceSnapshotParams{
		ID: "snap-1", TenantID: "tenant-1", Provenance: provenance(2),
	})
	if err != nil {
		t.Fatalf("фиксация снимка: %v", err)
	}

	if _, err := segment.ScoreLead(profile(t, "lead-2", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(3)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	if snapshot.MemberCount() != 1 {
		t.Fatalf("снимок изменился вместе с сегментом: размер %d", snapshot.MemberCount())
	}
}

// TestFixSnapshotTenantMismatch — снимок чужого тенанта не фиксируется.
func TestFixSnapshotTenantMismatch(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	_, err := domain.FixAudienceSnapshot(segment, domain.NewAudienceSnapshotParams{
		ID: "snap-1", TenantID: "tenant-2", Provenance: provenance(1),
	})
	if !errors.Is(err, domain.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}
}

// TestRehydrateAudienceSnapshot — восстановление снимка сохраняет состав и
// помечает его зафиксированным, не эмитя событий.
func TestRehydrateAudienceSnapshot(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	if _, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(1)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	snapshot, err := domain.FixAudienceSnapshot(segment, domain.NewAudienceSnapshotParams{
		ID: "snap-1", TenantID: "tenant-1", Provenance: provenance(2),
	})
	if err != nil {
		t.Fatalf("фиксация снимка: %v", err)
	}
	snapshot.PullDomainEvents()

	restored, err := domain.RehydrateAudienceSnapshot(snapshot.State())
	if err != nil {
		t.Fatalf("восстановление: %v", err)
	}
	if !restored.Frozen() || restored.MemberCount() != 1 {
		t.Fatalf("восстановленный снимок: frozen=%v members=%d", restored.Frozen(), restored.MemberCount())
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatalf("восстановление не должно эмитить события: %d", len(restored.DomainEvents()))
	}
}
