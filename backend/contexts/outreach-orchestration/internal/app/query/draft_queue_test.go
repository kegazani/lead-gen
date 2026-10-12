package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// riskyDraft помечает один черновик признаком риска: внешний предикат сообщает
// признаки, а запрос лишь показывает результат проверки.
func riskyDraft(id domain.DraftID) domain.CanBeDecidedInPackage {
	return func(_ context.Context, req domain.RiskFactsRequest) ([]domain.RiskReason, error) {
		if req.DraftID == id {
			return []domain.RiskReason{domain.RiskStopRuleTriggered}, nil
		}
		return nil, nil
	}
}

func TestDraftQueueShowsRiskPerDraftAndFilters(t *testing.T) {
	campaign := campaignWithDrafts(t)
	repo := newFakeRepository(campaign)

	handler := NewDraftQueueHandler(repo, repo, riskyDraft(domain.DraftID("draft-pending")), allowAccess())

	items, err := handler.Handle(context.Background(), DraftQueue{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// В очереди только черновик, ожидающий решения: утверждённый и отклонённый
	// исключены доменом.
	if len(items) != 1 {
		t.Fatalf("позиций очереди: got %d, want 1", len(items))
	}
	item := items[0]
	if item.DraftID != domain.DraftID("draft-pending") {
		t.Fatalf("черновик: got %q", item.DraftID)
	}
	if !item.Risky() || len(item.RiskReasons) != 1 || item.RiskReasons[0] != domain.RiskStopRuleTriggered {
		t.Fatalf("результат проверки риска не показан: %+v", item.RiskReasons)
	}
	if item.CampaignID != campaign.ID() {
		t.Fatalf("кампания позиции: got %q", item.CampaignID)
	}
}

func TestDraftQueueRiskFilterRiskyExcludesSafe(t *testing.T) {
	campaign := newCampaign(t, domain.CampaignID("campaign-1"), domain.TenantID("tenant-1"))
	mustDraft(t, campaign, domain.DraftID("draft-risky"), testSpec(t, domain.ActionChannelEmailName))
	mustDraft(t, campaign, domain.DraftID("draft-safe"), testSpec(t, domain.ActionChannelCallName))
	repo := newFakeRepository(campaign)

	handler := NewDraftQueueHandler(repo, repo, riskyDraft(domain.DraftID("draft-risky")), allowAccess())

	risky, err := handler.Handle(context.Background(), DraftQueue{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
		Risk:     RiskFilterRisky,
	})
	if err != nil {
		t.Fatalf("Handle risky: %v", err)
	}
	if len(risky) != 1 || risky[0].DraftID != domain.DraftID("draft-risky") {
		t.Fatalf("фильтр risky вернул %+v", risky)
	}

	safe, err := handler.Handle(context.Background(), DraftQueue{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
		Risk:     RiskFilterSafe,
	})
	if err != nil {
		t.Fatalf("Handle safe: %v", err)
	}
	if len(safe) != 1 || safe[0].DraftID != domain.DraftID("draft-safe") {
		t.Fatalf("фильтр safe вернул %+v", safe)
	}
}

func TestDraftQueueChannelFilter(t *testing.T) {
	campaign := newCampaign(t, domain.CampaignID("campaign-1"), domain.TenantID("tenant-1"))
	mustDraft(t, campaign, domain.DraftID("draft-email"), testSpec(t, domain.ActionChannelEmailName))
	mustDraft(t, campaign, domain.DraftID("draft-call"), testSpec(t, domain.ActionChannelCallName))
	repo := newFakeRepository(campaign)

	channel, err := domain.ParseActionChannel(domain.ActionChannelCallName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	handler := NewDraftQueueHandler(repo, repo, riskFor(), allowAccess())
	items, err := handler.Handle(context.Background(), DraftQueue{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
		Channel:  channel,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(items) != 1 || items[0].DraftID != domain.DraftID("draft-call") {
		t.Fatalf("фильтр канала вернул %+v", items)
	}
}

func TestDraftQueueExcludesForeignTenant(t *testing.T) {
	own := campaignWithDrafts(t)
	foreign := newCampaign(t, domain.CampaignID("campaign-2"), domain.TenantID("tenant-2"))
	mustDraft(t, foreign, domain.DraftID("draft-foreign"), testSpec(t, domain.ActionChannelEmailName))
	repo := newFakeRepository(own, foreign)

	handler := NewDraftQueueHandler(repo, repo, riskFor(), allowAccess())
	items, err := handler.Handle(context.Background(), DraftQueue{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	for _, item := range items {
		if item.CampaignID == foreign.ID() {
			t.Fatalf("в очередь попал чужой тенант: %+v", item)
		}
	}
}

func TestDraftQueueRejectsUnknownRiskFilter(t *testing.T) {
	campaign := campaignWithDrafts(t)
	repo := newFakeRepository(campaign)
	handler := NewDraftQueueHandler(repo, repo, riskFor(), allowAccess())
	_, err := handler.Handle(context.Background(), DraftQueue{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
		Risk:     RiskFilter("что-то"),
	})
	if !errors.Is(err, ErrInvalidRiskFilter) {
		t.Fatalf("ожидалась ErrInvalidRiskFilter, получено %v", err)
	}
}

func TestDraftQueueRequiresAccess(t *testing.T) {
	campaign := campaignWithDrafts(t)
	repo := newFakeRepository(campaign)
	handler := NewDraftQueueHandler(repo, repo, riskFor(), &stubAccess{allowed: false})
	_, err := handler.Handle(context.Background(), DraftQueue{
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидалась ErrAccessDenied, получено %v", err)
	}
}
