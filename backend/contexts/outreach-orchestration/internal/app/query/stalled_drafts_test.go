package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func TestStalledDraftsListsExpiredWithoutAutoApproval(t *testing.T) {
	campaign := newCampaign(t, domain.CampaignID("campaign-1"), domain.TenantID("tenant-1"))
	mustDraft(t, campaign, domain.DraftID("draft-expired"), testSpec(t, domain.ActionChannelEmailName))
	mustDraft(t, campaign, domain.DraftID("draft-pending"), testSpec(t, domain.ActionChannelCallName))
	// Срок первого черновика истёк: домен помечает его просроченным, но не
	// утверждает — автоутверждение запрещено.
	if err := campaign.ExpireDraft(domain.DraftID("draft-expired"), 1); err != nil {
		t.Fatalf("ExpireDraft: %v", err)
	}
	campaign.PullDomainEvents()
	repo := newFakeRepository(campaign)

	handler := NewStalledDraftsHandler(repo, repo, allowAccess())
	result, err := handler.Handle(context.Background(), StalledDrafts{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("зависших черновиков: got %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.DraftID != domain.DraftID("draft-expired") {
		t.Fatalf("черновик: got %q", item.DraftID)
	}
	if item.Executed {
		t.Fatal("просроченный черновик не должен быть исполнен: автоутверждения нет")
	}
	if item.State != domain.DraftStateExpired {
		t.Fatalf("состояние: got %q", item.State)
	}
	if !result.Notify {
		t.Fatal("оператор должен получить уведомление о зависшем черновике")
	}
}

func TestStalledDraftsNotifyFalseWhenNothingStalled(t *testing.T) {
	campaign := campaignWithDrafts(t)
	repo := newFakeRepository(campaign)
	handler := NewStalledDraftsHandler(repo, repo, allowAccess())
	result, err := handler.Handle(context.Background(), StalledDrafts{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("зависших черновиков быть не должно: %+v", result.Items)
	}
	if result.Notify {
		t.Fatal("без зависших черновиков уведомление не выставляется")
	}
}

func TestStalledDraftsRequiresAccess(t *testing.T) {
	campaign := campaignWithDrafts(t)
	repo := newFakeRepository(campaign)
	handler := NewStalledDraftsHandler(repo, repo, &stubAccess{allowed: false})
	_, err := handler.Handle(context.Background(), StalledDrafts{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидалась ErrAccessDenied, получено %v", err)
	}
}
