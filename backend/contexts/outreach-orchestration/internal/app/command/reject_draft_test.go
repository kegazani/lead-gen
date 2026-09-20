package command

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func rejectCommand() RejectDraft {
	return RejectDraft{
		CampaignID:      domain.CampaignID("campaign-1"),
		TenantID:        domain.TenantID("tenant-1"),
		Actor:           domain.ActorID("operator-1"),
		DraftID:         domain.DraftID("draft-1"),
		DecisionID:      domain.DecisionID("decision-1"),
		ExpectedVersion: 1,
		Reason:          "нужен другой оффер",
	}
}

func TestRejectDraftRecordsDecisionWithoutApprovalEvent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	result, err := NewRejectDraftHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), rejectCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Outcome != domain.DecisionRejected {
		t.Fatalf("результат решения: got %q, want %q", result.Outcome, domain.DecisionRejected)
	}
	if countApprovedEvents(kit.outbox.events) != 0 {
		t.Fatal("отклонение не порождает action-approved")
	}
	draft, _ := kit.campaign(t).Draft(domain.DraftID("draft-1"))
	if draft.State() != domain.DraftStateRejected {
		t.Fatalf("состояние черновика: got %q, want %q", draft.State(), domain.DraftStateRejected)
	}
}

func TestRejectDraftRequiresReason(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	command := rejectCommand()
	command.Reason = "  "
	_, err := NewRejectDraftHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), command)
	if !errors.Is(err, domain.ErrDecisionReasonRequired) {
		t.Fatalf("ожидалось требование причины, получено %v", err)
	}
	draft, _ := kit.campaign(t).Draft(domain.DraftID("draft-1"))
	if draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("состояние не должно меняться: got %q", draft.State())
	}
}

func TestRejectDraftRepeatIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))
	handler := NewRejectDraftHandler(kit.access, kit.clock, kit.uow)
	command := rejectCommand()

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
}
