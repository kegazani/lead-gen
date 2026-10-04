package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func postponeCommand(until time.Time) PostponeDraft {
	return PostponeDraft{
		CampaignID:      domain.CampaignID("campaign-1"),
		TenantID:        domain.TenantID("tenant-1"),
		Actor:           domain.ActorID("operator-1"),
		DraftID:         domain.DraftID("draft-1"),
		ExpectedVersion: 1,
		Until:           until,
	}
}

func TestPostponeDraftKeepsDraftPending(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))
	until := testNow().Add(48 * time.Hour)

	result, err := NewPostponeDraftHandler(kit.access, kit.uow).Handle(
		context.Background(), postponeCommand(until),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.State != domain.DraftStatePendingApproval {
		t.Fatalf("состояние: got %q, want %q", result.State, domain.DraftStatePendingApproval)
	}
	if !result.PostponedUntil.Equal(until.UTC()) {
		t.Fatalf("срок отложения: got %v, want %v", result.PostponedUntil, until.UTC())
	}
	// Отложение не является решением и не исполняет действие.
	campaign := kit.campaign(t)
	if len(campaign.DecisionsForDraft(domain.DraftID("draft-1"))) != 0 {
		t.Fatal("отложение не должно создавать решение")
	}
	if countApprovedEvents(kit.outbox.events) != 0 {
		t.Fatal("отложение не должно порождать action-approved")
	}
}

func TestPostponeDraftRequiresDeadline(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	_, err := NewPostponeDraftHandler(kit.access, kit.uow).Handle(
		context.Background(), postponeCommand(time.Time{}),
	)
	if !errors.Is(err, domain.ErrPostponeDeadlineRequired) {
		t.Fatalf("ожидалась ErrPostponeDeadlineRequired, получено %v", err)
	}
}

func TestPostponeDraftRejectsStaleVersion(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	command := postponeCommand(testNow().Add(time.Hour))
	command.ExpectedVersion = 99
	_, err := NewPostponeDraftHandler(kit.access, kit.uow).Handle(context.Background(), command)
	if !errors.Is(err, domain.ErrDraftVersionMismatch) {
		t.Fatalf("ожидался отказ по версии, получено %v", err)
	}
}

func TestPostponeDraftDeniesAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	kit.access.allowed = false
	_, err := NewPostponeDraftHandler(kit.access, kit.uow).Handle(
		context.Background(), postponeCommand(testNow().Add(time.Hour)),
	)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидалась ErrAccessDenied, получено %v", err)
	}
}
