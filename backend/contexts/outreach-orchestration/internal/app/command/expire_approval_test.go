package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func expireCommand(deadline time.Time) ExpireApproval {
	return ExpireApproval{
		CampaignID:      domain.CampaignID("campaign-1"),
		TenantID:        domain.TenantID("tenant-1"),
		Actor:           domain.ActorID("operator-1"),
		DraftID:         domain.DraftID("draft-1"),
		ExpectedVersion: 1,
		Deadline:        deadline,
	}
}

func TestExpireApprovalMarksExpiredWithoutAutoApproval(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	deadline := kit.clock.now.Add(-time.Hour)
	result, err := NewExpireApprovalHandler(kit.access, kit.clock, kit.uow).Handle(
		context.Background(), expireCommand(deadline),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Expired || !result.Notify {
		t.Fatalf("ожидалось истечение с уведомлением, got %+v", result)
	}
	if result.State != domain.DraftStateExpired {
		t.Fatalf("состояние черновика: got %q, want %q", result.State, domain.DraftStateExpired)
	}
	if result.State == domain.DraftStateApproved {
		t.Fatal("автоутверждение запрещено")
	}

	campaign := kit.campaign(t)
	draft, _ := campaign.Draft(domain.DraftID("draft-1"))
	if draft.State() == domain.DraftStateApproved {
		t.Fatal("автоутверждение запрещено")
	}
	if err := campaign.ExecuteDraft(domain.DraftID("draft-1"), 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("просроченный черновик не должен исполняться, получено %v", err)
	}
}

func TestExpireApprovalBeforeDeadlineChangesNothing(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))
	savesBefore := kit.repo.saves

	deadline := kit.clock.now.Add(time.Hour)
	result, err := NewExpireApprovalHandler(kit.access, kit.clock, kit.uow).Handle(
		context.Background(), expireCommand(deadline),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Expired {
		t.Fatal("до срока черновик не помечается просроченным")
	}
	if result.State != domain.DraftStatePendingApproval {
		t.Fatalf("состояние черновика: got %q", result.State)
	}
	if kit.repo.saves != savesBefore {
		t.Fatalf("до срока агрегат не меняется, saves=%d", kit.repo.saves)
	}
}

func TestExpireApprovalRequiresDeadline(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	_, err := NewExpireApprovalHandler(kit.access, kit.clock, kit.uow).Handle(
		context.Background(), expireCommand(time.Time{}),
	)
	if !errors.Is(err, ErrApprovalDeadlineRequired) {
		t.Fatalf("ожидался отказ без срока, получено %v", err)
	}
}

func TestExpireApprovalDeniesWithoutAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))
	kit.access.allowed = false

	_, err := NewExpireApprovalHandler(kit.access, kit.clock, kit.uow).Handle(
		context.Background(), expireCommand(kit.clock.now.Add(-time.Hour)),
	)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидался отказ доступа, получено %v", err)
	}
	draft, _ := kit.campaign(t).Draft(domain.DraftID("draft-1"))
	if draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("без права доступа черновик не меняется: got %q", draft.State())
	}
}
