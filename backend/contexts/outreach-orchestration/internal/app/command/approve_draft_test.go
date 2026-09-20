package command

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func approveCommand() ApproveDraft {
	return ApproveDraft{
		CampaignID:      domain.CampaignID("campaign-1"),
		TenantID:        domain.TenantID("tenant-1"),
		Actor:           domain.ActorID("operator-1"),
		DraftID:         domain.DraftID("draft-1"),
		DecisionID:      domain.DecisionID("decision-1"),
		ExpectedVersion: 1,
		Reason:          "согласовано",
	}
}

func TestApproveDraftRecordsDecisionAndEvent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	result, err := NewApproveDraftHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), approveCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Recorded {
		t.Fatal("первое решение должно быть записано")
	}
	if result.Outcome != domain.DecisionApproved {
		t.Fatalf("результат решения: got %q, want %q", result.Outcome, domain.DecisionApproved)
	}
	if result.DraftVersion != 1 {
		t.Fatalf("версия решения: got %d, want 1", result.DraftVersion)
	}

	draft, ok := kit.campaign(t).Draft(domain.DraftID("draft-1"))
	if !ok {
		t.Fatal("черновик не найден")
	}
	if draft.State() != domain.DraftStateApproved {
		t.Fatalf("состояние черновика: got %q, want %q", draft.State(), domain.DraftStateApproved)
	}
	if countApprovedEvents(kit.outbox.events) != 1 {
		t.Fatalf("ожидалось одно action-approved, got %d", countApprovedEvents(kit.outbox.events))
	}
}

func TestApproveDraftRepeatIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))
	handler := NewApproveDraftHandler(kit.access, kit.clock, kit.uow)
	command := approveCommand()

	if _, err := handler.Handle(context.Background(), command); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	result, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("повторный Handle: %v", err)
	}
	if result.Recorded {
		t.Fatal("повтор решения не должен создавать вторую запись")
	}
	if got := len(kit.campaign(t).DecisionsForDraft(domain.DraftID("draft-1"))); got != 1 {
		t.Fatalf("решений по черновику: got %d, want 1", got)
	}
	if countApprovedEvents(kit.outbox.events) != 1 {
		t.Fatalf("повтор не должен порождать второе action-approved, got %d", countApprovedEvents(kit.outbox.events))
	}
}

func TestApproveDraftRejectsStaleVersion(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	command := approveCommand()
	command.ExpectedVersion = 2
	_, err := NewApproveDraftHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), command)
	if !errors.Is(err, domain.ErrDraftVersionMismatch) {
		t.Fatalf("ожидался отказ по версии, получено %v", err)
	}
	draft, _ := kit.campaign(t).Draft(domain.DraftID("draft-1"))
	if draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("состояние не должно меняться: got %q", draft.State())
	}
}

func TestApproveDraftDeniesWithoutAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))
	kit.access.allowed = false

	_, err := NewApproveDraftHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), approveCommand())
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидался отказ доступа, получено %v", err)
	}
	draft, _ := kit.campaign(t).Draft(domain.DraftID("draft-1"))
	if draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("без права доступа решение не принимается: got %q", draft.State())
	}
}
