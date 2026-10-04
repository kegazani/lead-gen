package query

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func queryStep(t *testing.T) domain.SequenceStep {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	step, err := domain.NewSequenceStep(domain.SequenceStepParams{
		Action:   channel,
		Material: "material-1",
		Segment:  "segment-1",
	})
	if err != nil {
		t.Fatalf("NewSequenceStep: %v", err)
	}
	return step
}

func campaignWithSequence(t *testing.T) *domain.Campaign {
	t.Helper()
	campaign := newCampaign(t, "campaign-1", "tenant-1")
	if _, err := campaign.DefineSequence(domain.SequenceDefinitionParams{
		ID:       "sequence-1",
		Campaign: campaign.ID(),
		Tenant:   campaign.TenantID(),
		Name:     "Сценарий",
		Steps:    []domain.SequenceStep{queryStep(t), queryStep(t)},
	}); err != nil {
		t.Fatalf("DefineSequence: %v", err)
	}
	if _, err := campaign.EnrollLead(domain.EnrollmentParams{
		ExecutionID: "execution-1",
		SequenceID:  "sequence-1",
		TenantID:    campaign.TenantID(),
		Lead:        "lead-1",
		Recipient:   "lead-1",
		At:          testNow(),
	}); err != nil {
		t.Fatalf("EnrollLead: %v", err)
	}
	if _, err := campaign.AdvanceSequences(testNow(), domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if _, _, err := campaign.ApproveDraft(domain.DecisionParams{
		DecisionID:   "decision-1",
		DraftID:      "execution-1.step-0",
		DraftVersion: 1,
		Actor:        "operator-1",
		DecidedAt:    testNow(),
	}); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	if _, err := campaign.AdvanceSequences(testNow(), domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	campaign.PullDomainEvents()
	return campaign
}

func TestSequenceHistoryByLead(t *testing.T) {
	campaign := campaignWithSequence(t)
	handler := NewSequenceHistoryHandler(newFakeRepository(campaign), allowAccess())

	entries, err := handler.Handle(context.Background(), SequenceHistory{
		CampaignIDs: []domain.CampaignID{"campaign-1"},
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		Lead:        "lead-1",
	})
	if err != nil {
		t.Fatalf("SequenceHistory.Handle: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("история шагов по лиду не должна быть пустой")
	}
	hasApprover := false
	for _, entry := range entries {
		if entry.Result == domain.OutcomeApproved && entry.Actor == "operator-1" {
			hasApprover = true
		}
	}
	if !hasApprover {
		t.Fatal("в истории должен быть шаг с утвердившим оператором")
	}
}

func TestSequenceStateShowsPendingDrafts(t *testing.T) {
	campaign := campaignWithSequence(t)
	handler := NewSequenceStateHandler(newFakeRepository(campaign), allowAccess())

	items, err := handler.Handle(context.Background(), SequenceState{
		CampaignIDs: []domain.CampaignID{"campaign-1"},
		TenantID:    "tenant-1",
		Actor:       "operator-1",
	})
	if err != nil {
		t.Fatalf("SequenceState.Handle: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ожидалось состояние одной кампании, получено %d", len(items))
	}
	if len(items[0].Executions) != 1 {
		t.Fatalf("ожидалось одно участие, получено %d", len(items[0].Executions))
	}
	if items[0].PendingDrafts != 1 {
		t.Fatalf("неутверждённых черновиков цепочек должно быть 1, получено %d", items[0].PendingDrafts)
	}
}
