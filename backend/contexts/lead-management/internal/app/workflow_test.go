package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

func TestChangeLeadStageMovesLeadAndRecordsActivity(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "Example Corp")
	activity := newMemActivity()

	u := &ChangeLeadStage{
		Profiles: profiles,
		Activity: activity,
		Clock:    fakeClock{t: testTime},
	}

	if err := u.Change(context.Background(), "example.com", domain.StageEmailSent, "оператор"); err != nil {
		t.Fatalf("Change: %v", err)
	}

	company := profiles.byID["example.com"]
	if company.Status().Stage != domain.StageEmailSent {
		t.Fatalf("стадия %q, ожидалась %q", company.Status().Stage, domain.StageEmailSent)
	}

	entries := activity.entries
	if len(entries) != 1 {
		t.Fatalf("записей перехода %d, ожидалась 1", len(entries))
	}
	if entries[0].LeadID != "example.com" || entries[0].Stage != domain.StageEmailSent || entries[0].Author != "оператор" {
		t.Fatalf("запись перехода = %+v", entries[0])
	}
	if !entries[0].At.Equal(testTime) {
		t.Fatalf("время перехода %v, ожидалось %v", entries[0].At, testTime)
	}
}

func TestChangeLeadStageRejectsUnknownStage(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "Example Corp")

	u := &ChangeLeadStage{Profiles: profiles, Activity: newMemActivity(), Clock: fakeClock{t: testTime}}
	if err := u.Change(context.Background(), "example.com", domain.Stage("nope"), "оператор"); !errors.Is(err, domain.ErrInvalidStage) {
		t.Fatalf("ожидалась ErrInvalidStage, получено %v", err)
	}
}

func TestSnoozeLeadHidesUntilDeadline(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "Example Corp")

	u := &SnoozeLead{Profiles: profiles, Clock: fakeClock{t: testTime}}
	until := testTime.Add(24 * time.Hour)
	if err := u.Snooze(context.Background(), "example.com", until); err != nil {
		t.Fatalf("Snooze: %v", err)
	}

	company := profiles.byID["example.com"]
	if company.Status().SnoozeUntil == nil || !company.Status().SnoozeUntil.Equal(until) {
		t.Fatalf("SnoozeUntil = %v, ожидался %v", company.Status().SnoozeUntil, until)
	}
}

func TestReturnExpiredReturnsLeadToPool(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "expired.example.com", "Expired Corp")
	seedCompany(profiles, "future.example.com", "Future Corp")

	now := testTime
	// Истёкший срок.
	profiles.byID["expired.example.com"].Snooze(now.Add(-time.Hour), now)
	// Срок в будущем — не трогаем.
	profiles.byID["future.example.com"].Snooze(now.Add(time.Hour), now)

	u := &SnoozeLead{Profiles: profiles, Clock: fakeClock{t: now}}
	returned, err := u.ReturnExpired(context.Background())
	if err != nil {
		t.Fatalf("ReturnExpired: %v", err)
	}
	if returned != 1 {
		t.Fatalf("возвращено %d, ожидался 1", returned)
	}

	if profiles.byID["expired.example.com"].Status().SnoozeUntil != nil {
		t.Fatal("истёкший лид не возвращён в пул")
	}
	if profiles.byID["future.example.com"].Status().SnoozeUntil == nil {
		t.Fatal("лид с будущим сроком не должен быть возвращён")
	}
}
