package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

func TestDraftDecisionJournalReturnsAuthorTimeAndOutcome(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), allowAccess())

	entries, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		TenantID:   testTenant,
		DraftID:    domain.DraftID("draft-approved"),
		Actor:      domain.ActorID("operator-1"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей журнала: got %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Actor != domain.ActorID("operator-1") {
		t.Fatalf("автор решения: got %q", entry.Actor)
	}
	if entry.Outcome != domain.DecisionApproved {
		t.Fatalf("результат решения: got %q", entry.Outcome)
	}
	if entry.DraftVersion != 1 {
		t.Fatalf("версия решения: got %d, want 1", entry.DraftVersion)
	}
	if !entry.DecidedAt.Equal(testNow()) {
		t.Fatalf("время решения: got %s, want %s", entry.DecidedAt, testNow().UTC())
	}
}

func TestDraftDecisionJournalKeepsRejectionReason(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), allowAccess())

	entries, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		TenantID:   testTenant,
		DraftID:    domain.DraftID("draft-rejected"),
		Actor:      domain.ActorID("operator-2"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей журнала: got %d, want 1", len(entries))
	}
	if entries[0].Outcome != domain.DecisionRejected {
		t.Fatalf("результат решения: got %q", entries[0].Outcome)
	}
	if entries[0].Reason != "нет оффера" {
		t.Fatalf("причина решения: got %q", entries[0].Reason)
	}
}

func TestDraftDecisionJournalShowsDecisionsOfOtherAuthors(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), allowAccess())

	// Запрос делает operator-2, а решение по черновику принял operator-1.
	// Журнал обязан вернуть чужое решение: иначе аудит теряет записи.
	entries, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		TenantID:   testTenant,
		DraftID:    domain.DraftID("draft-approved"),
		Actor:      domain.ActorID("operator-2"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей журнала: got %d, want 1", len(entries))
	}
	if entries[0].Actor != domain.ActorID("operator-1") {
		t.Fatalf("автор решения: got %q, want operator-1", entries[0].Actor)
	}
}

func TestDraftDecisionJournalRejectsForeignTenant(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), allowAccess())

	_, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		TenantID:   domain.TenantID("tenant-2"),
		DraftID:    domain.DraftID("draft-approved"),
		Actor:      testOperator,
	})
	if !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Fatalf("ожидался отказ по тенанту, получено %v", err)
	}
}

func TestDraftDecisionJournalRequiresTenant(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), allowAccess())

	_, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		DraftID:    domain.DraftID("draft-approved"),
		Actor:      testOperator,
	})
	if !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("запрос без тенанта: получено %v, ожидалась ErrTenantRequired", err)
	}
}

func TestDraftDecisionJournalDeniesActorWithoutRight(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), &stubAccess{allowed: false})

	_, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		TenantID:   testTenant,
		DraftID:    domain.DraftID("draft-approved"),
		Actor:      testOperator,
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("актор без права: получено %v, ожидалась ErrAccessDenied", err)
	}
}
