package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewLeadStatusValidatesStage(t *testing.T) {
	for _, stage := range []Stage{StageNotWarmed, StageEmailSent, StageWarming, StageCold} {
		if _, err := NewLeadStatus(stage, nil); err != nil {
			t.Fatalf("стадия %q отвергнута: %v", stage, err)
		}
	}
	if _, err := NewLeadStatus(Stage("unknown"), nil); !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("ожидалась ErrInvalidStage, получено %v", err)
	}
}

func TestNewLeadStatusClonesSnooze(t *testing.T) {
	until := baseTime.Add(24 * time.Hour)
	status, err := NewLeadStatus(StageWarming, &until)
	if err != nil {
		t.Fatalf("NewLeadStatus: %v", err)
	}
	// Изменение исходного момента не должно задеть value object.
	until = until.Add(time.Hour)
	if !status.SnoozeUntil.Equal(baseTime.Add(24 * time.Hour)) {
		t.Fatalf("SnoozeUntil изменён снаружи: %v", status.SnoozeUntil)
	}
}

func TestCompanyChangeStage(t *testing.T) {
	c := mustStatusCompany(t)
	if err := c.ChangeStage(StageEmailSent, baseTime); err != nil {
		t.Fatalf("ChangeStage: %v", err)
	}
	if c.Status().Stage != StageEmailSent {
		t.Fatalf("Stage %q, ожидался %q", c.Status().Stage, StageEmailSent)
	}
	if err := c.ChangeStage(Stage("bogus"), baseTime); !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("ожидалась ErrInvalidStage, получено %v", err)
	}
}

func TestCompanySnoozeAndReturnToPool(t *testing.T) {
	c := mustStatusCompany(t)
	until := baseTime.Add(24 * time.Hour)
	c.Snooze(until, baseTime)

	status := c.Status()
	if status.SnoozeUntil == nil || !status.SnoozeUntil.Equal(until) {
		t.Fatalf("SnoozeUntil %v, ожидался %v", status.SnoozeUntil, until)
	}
	if status.InPool(baseTime) {
		t.Fatal("отложенный лид до срока должен быть вне активной воронки")
	}
	if !status.InPool(until) {
		t.Fatal("лид по наступлении срока должен вернуться в активную воронку")
	}

	c.ReturnToPool(baseTime.Add(time.Hour))
	if s := c.Status(); s.SnoozeUntil != nil || !s.InPool(baseTime) {
		t.Fatalf("после ReturnToPool лид должен быть в активной воронке: %+v", s)
	}
}

func TestCompanyInitialStatusNotWarmed(t *testing.T) {
	c := mustStatusCompany(t)
	if c.Status().Stage != StageNotWarmed {
		t.Fatalf("начальная стадия %q, ожидалась %q", c.Status().Stage, StageNotWarmed)
	}
}

func TestLeadStatusSnapshotRoundTrip(t *testing.T) {
	until := baseTime.Add(48 * time.Hour)
	status, err := NewLeadStatus(StageCold, &until)
	if err != nil {
		t.Fatalf("NewLeadStatus: %v", err)
	}
	rehydrated, err := RehydrateLeadStatus(status.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateLeadStatus: %v", err)
	}
	if rehydrated.Stage != status.Stage {
		t.Fatalf("Stage %q, ожидался %q", rehydrated.Stage, status.Stage)
	}
	if !rehydrated.SnoozeUntil.Equal(until) {
		t.Fatalf("SnoozeUntil %v, ожидался %v", rehydrated.SnoozeUntil, until)
	}
}

func TestRehydrateLeadStatusRejectsContradictorySnapshot(t *testing.T) {
	if _, err := RehydrateLeadStatus(LeadStatus{Stage: "nope"}); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ожидалась ErrInvalidSnapshot, получено %v", err)
	}
}

func mustStatusCompany(t *testing.T) *Company {
	t.Helper()
	c, err := CreateCompany(CreateCompanyParams{
		Domain:    "example.com",
		Name:      AttributedValue{Value: "Example", Provenance: provenance("https://example.com")},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	return c
}
