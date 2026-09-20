package query

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// Локальные тест-дубли портов: query-тесты не зависят от adapters.

func testNow() time.Time {
	return time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
}

// stubAccess — управляемый порт проверки доступа для читающих путей.
type stubAccess struct {
	allowed bool
	err     error
	calls   int
}

func (s *stubAccess) CanAct(_ context.Context, _ domain.ActorID, _ domain.TenantID) (bool, error) {
	s.calls++
	return s.allowed, s.err
}

func allowAccess() *stubAccess { return &stubAccess{allowed: true} }

type fakeRepository struct {
	campaigns map[domain.CampaignID]*domain.Campaign
}

func newFakeRepository(campaigns ...*domain.Campaign) *fakeRepository {
	repo := &fakeRepository{campaigns: make(map[domain.CampaignID]*domain.Campaign)}
	for _, campaign := range campaigns {
		repo.campaigns[campaign.ID()] = campaign
	}
	return repo
}

func (r *fakeRepository) Save(_ context.Context, campaign *domain.Campaign) error {
	r.campaigns[campaign.ID()] = campaign
	return nil
}

func (r *fakeRepository) FindByID(_ context.Context, id domain.CampaignID) (*domain.Campaign, error) {
	campaign, ok := r.campaigns[id]
	if !ok {
		return nil, ports.ErrCampaignNotFound
	}
	return campaign, nil
}

func testSpec(t *testing.T, channelName string) domain.DraftSpec {
	t.Helper()
	channel, err := domain.ParseActionChannel(channelName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	return domain.DraftSpec{
		Material:  domain.MaterialID("material-1"),
		Segment:   domain.SegmentID("segment-1"),
		Recipient: domain.RecipientID("recipient-1"),
		Channel:   channel,
	}
}

func newCampaign(t *testing.T, id domain.CampaignID, tenant domain.TenantID) *domain.Campaign {
	t.Helper()
	window, err := domain.NewCampaignWindow(testNow(), testNow().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       id,
		TenantID: tenant,
		Name:     "Кампания " + string(id),
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return campaign
}

func mustDraft(t *testing.T, campaign *domain.Campaign, id domain.DraftID, spec domain.DraftSpec) {
	t.Helper()
	if _, _, err := campaign.DraftAction(id, spec, testNow()); err != nil {
		t.Fatalf("DraftAction %s: %v", id, err)
	}
	campaign.PullDomainEvents()
}

// campaignWithDrafts готовит кампанию с тремя черновиками: утверждённым,
// ожидающим решения и отклонённым.
func campaignWithDrafts(t *testing.T) *domain.Campaign {
	t.Helper()
	campaign := newCampaign(t, domain.CampaignID("campaign-1"), domain.TenantID("tenant-1"))

	mustDraft(t, campaign, domain.DraftID("draft-approved"), testSpec(t, domain.ActionChannelEmailName))
	if _, _, err := campaign.ApproveDraft(domain.DecisionParams{
		DecisionID:   domain.DecisionID("decision-approved"),
		DraftID:      domain.DraftID("draft-approved"),
		DraftVersion: 1,
		Actor:        domain.ActorID("operator-1"),
		DecidedAt:    testNow(),
	}); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	campaign.PullDomainEvents()

	mustDraft(t, campaign, domain.DraftID("draft-pending"), testSpec(t, domain.ActionChannelCallName))

	mustDraft(t, campaign, domain.DraftID("draft-rejected"), testSpec(t, domain.ActionChannelFormName))
	if _, _, err := campaign.RejectDraft(domain.DecisionParams{
		DecisionID:   domain.DecisionID("decision-rejected"),
		DraftID:      domain.DraftID("draft-rejected"),
		DraftVersion: 1,
		Actor:        domain.ActorID("operator-2"),
		Reason:       "нет оффера",
		DecidedAt:    testNow(),
	}); err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	campaign.PullDomainEvents()

	return campaign
}
