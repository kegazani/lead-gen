package command

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func TestDraftActionPlacesDraftPendingApproval(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)

	result, err := NewDraftActionHandler(kit.access, kit.clock, kit.uow).Handle(
		context.Background(), kit.draftCommand(t, testSpec(t)),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Created {
		t.Fatal("первая команда должна создать черновик")
	}
	if result.State != domain.DraftStatePendingApproval {
		t.Fatalf("состояние черновика: got %q, want %q", result.State, domain.DraftStatePendingApproval)
	}
	if result.Version != 1 {
		t.Fatalf("версия черновика: got %d, want 1", result.Version)
	}
	if countApprovedEvents(kit.outbox.events) != 0 {
		t.Fatal("постановка черновика не порождает action-approved")
	}
}

func TestDraftActionRepeatIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	handler := NewDraftActionHandler(kit.access, kit.clock, kit.uow)
	command := kit.draftCommand(t, testSpec(t))

	if _, err := handler.Handle(context.Background(), command); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	result, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("повторный Handle: %v", err)
	}
	if result.Created {
		t.Fatal("повтор команды не должен создавать второй черновик")
	}
	if got := len(kit.campaign(t).Drafts()); got != 1 {
		t.Fatalf("черновиков в кампании: got %d, want 1", got)
	}
}

func TestDraftActionRejectsDifferentSpecForSameDraft(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	handler := NewDraftActionHandler(kit.access, kit.clock, kit.uow)

	if _, err := handler.Handle(context.Background(), kit.draftCommand(t, testSpec(t))); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}

	other := testSpec(t)
	channel, err := domain.ParseActionChannel(domain.ActionChannelCallName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	other.Channel = channel

	_, err = handler.Handle(context.Background(), kit.draftCommand(t, other))
	if !errors.Is(err, domain.ErrDuplicateDraft) {
		t.Fatalf("ожидался конфликт черновика, получено %v", err)
	}
}

func TestDraftActionDeniesWithoutAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.access.allowed = false

	_, err := NewDraftActionHandler(kit.access, kit.clock, kit.uow).Handle(
		context.Background(), kit.draftCommand(t, testSpec(t)),
	)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидался отказ доступа, получено %v", err)
	}
	if len(kit.campaign(t).Drafts()) != 0 {
		t.Fatal("без права доступа черновик не ставится")
	}
}
