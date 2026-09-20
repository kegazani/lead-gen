package command

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func revisedSpec(t *testing.T) domain.DraftSpec {
	t.Helper()
	spec := testSpec(t)
	channel, err := domain.ParseActionChannel(domain.ActionChannelCallName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	spec.Channel = channel
	return spec
}

func reviseCommand(t *testing.T, spec domain.DraftSpec) ReviseDraft {
	t.Helper()
	return ReviseDraft{
		CampaignID:      domain.CampaignID("campaign-1"),
		TenantID:        domain.TenantID("tenant-1"),
		Actor:           domain.ActorID("operator-1"),
		DraftID:         domain.DraftID("draft-1"),
		ExpectedVersion: 1,
		Spec:            spec,
	}
}

func TestReviseDraftIncrementsVersionAndReturnsToApproval(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	result, err := NewReviseDraftHandler(kit.access, kit.uow).Handle(
		context.Background(), reviseCommand(t, revisedSpec(t)),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Revised {
		t.Fatal("первая доработка должна примениться")
	}
	if result.Version != 2 {
		t.Fatalf("версия черновика: got %d, want 2", result.Version)
	}
	if result.State != domain.DraftStatePendingApproval {
		t.Fatalf("состояние черновика: got %q, want %q", result.State, domain.DraftStatePendingApproval)
	}
	if len(kit.campaign(t).DecisionsForDraft(domain.DraftID("draft-1"))) != 0 {
		t.Fatal("доработка не создаёт решение")
	}
}

func TestReviseDraftRepeatIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))
	handler := NewReviseDraftHandler(kit.access, kit.uow)
	command := reviseCommand(t, revisedSpec(t))

	if _, err := handler.Handle(context.Background(), command); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	result, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("повторный Handle: %v", err)
	}
	if result.Revised {
		t.Fatal("повтор доработки не должен повышать версию второй раз")
	}
	if result.Version != 2 {
		t.Fatalf("версия черновика: got %d, want 2", result.Version)
	}
}

func TestReviseDraftRejectsStaleVersion(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.mustDraft(t, testSpec(t))

	command := reviseCommand(t, revisedSpec(t))
	command.ExpectedVersion = 99
	_, err := NewReviseDraftHandler(kit.access, kit.uow).Handle(context.Background(), command)
	if !errors.Is(err, domain.ErrDraftVersionMismatch) {
		t.Fatalf("ожидался отказ по версии, получено %v", err)
	}
	draft, _ := kit.campaign(t).Draft(domain.DraftID("draft-1"))
	if draft.Version() != 1 {
		t.Fatalf("версия не должна меняться: got %d", draft.Version())
	}
}
