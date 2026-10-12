package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

const (
	approvalTenant = domain.TenantID("tenant-1")
	approvalDraft  = domain.DraftID("company-1@v1")
)

// campaignWithDecision готовит кампанию подачи с черновиком агента и решением по нему.
func campaignWithDecision(t *testing.T, decide func(*domain.Campaign)) *domain.Campaign {
	t.Helper()
	campaign := newCampaign(t, domain.DraftCampaignID(approvalTenant, approvalDraft), approvalTenant)
	mustDraft(t, campaign, approvalDraft, testSpec(t, domain.ActionChannelEmailName))
	if decide != nil {
		decide(campaign)
		campaign.PullDomainEvents()
	}
	return campaign
}

func approveCurrent(t *testing.T, draftID domain.DraftID, decisionID domain.DecisionID) func(*domain.Campaign) {
	t.Helper()
	return func(campaign *domain.Campaign) {
		if _, _, err := campaign.ApproveDraft(domain.DecisionParams{
			DecisionID:   decisionID,
			DraftID:      draftID,
			DraftVersion: 1,
			Actor:        domain.ActorID("operator-1"),
			DecidedAt:    testNow(),
		}); err != nil {
			t.Fatalf("ApproveDraft: %v", err)
		}
	}
}

func TestApprovedActionReturnsActionMatchingDecision(t *testing.T) {
	campaign := campaignWithDecision(t, approveCurrent(t, approvalDraft, "decision-1"))
	handler := NewApprovedActionHandler(newFakeRepository(campaign))

	action, err := handler.Handle(context.Background(), ApprovedActionQuery{
		TenantID:     approvalTenant,
		DraftRef:     approvalDraft,
		DraftVersion: 1,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("действие невалидно: %v", err)
	}
	if action.DraftID != string(approvalDraft) {
		t.Fatalf("ссылка черновика = %q", action.DraftID)
	}
	if action.DraftVersion != 1 {
		t.Fatalf("версия = %d, ожидалась 1", action.DraftVersion)
	}
	if action.DecisionID != "decision-1" {
		t.Fatalf("решение = %q", action.DecisionID)
	}
	if action.CampaignID != string(campaign.ID()) {
		t.Fatalf("кампания = %q", action.CampaignID)
	}
	if action.Channel != domain.ActionChannelEmailName {
		t.Fatalf("канал = %q", action.Channel)
	}
	if action.IdempotencyKey() == "" {
		t.Fatal("пустой ключ идемпотентности")
	}
}

func TestApprovedActionRejectsRejectedDecision(t *testing.T) {
	campaign := campaignWithDecision(t, func(campaign *domain.Campaign) {
		if _, _, err := campaign.RejectDraft(domain.DecisionParams{
			DecisionID:   "decision-1",
			DraftID:      approvalDraft,
			DraftVersion: 1,
			Actor:        domain.ActorID("operator-1"),
			Reason:       "нет оффера",
			DecidedAt:    testNow(),
		}); err != nil {
			t.Fatalf("RejectDraft: %v", err)
		}
	})
	handler := NewApprovedActionHandler(newFakeRepository(campaign))

	if _, err := handler.Handle(context.Background(), ApprovedActionQuery{
		TenantID: approvalTenant, DraftRef: approvalDraft, DraftVersion: 1,
	}); !errors.Is(err, ErrDraftDecisionRejected) {
		t.Fatalf("ожидалась ErrDraftDecisionRejected, получено %v", err)
	}
}

func TestApprovedActionReportsPending(t *testing.T) {
	campaign := campaignWithDecision(t, nil)
	handler := NewApprovedActionHandler(newFakeRepository(campaign))

	if _, err := handler.Handle(context.Background(), ApprovedActionQuery{
		TenantID: approvalTenant, DraftRef: approvalDraft, DraftVersion: 1,
	}); !errors.Is(err, ErrDraftDecisionPending) {
		t.Fatalf("ожидалась ErrDraftDecisionPending, получено %v", err)
	}
}

func TestApprovedActionDoesNotReturnStaleVersionAfterRevision(t *testing.T) {
	campaign := campaignWithDecision(t, func(campaign *domain.Campaign) {
		approveCurrent(t, approvalDraft, "decision-1")(campaign)
		// Оператор доработал черновик: решение v1 к новой версии не относится.
		reviseDraft(t, campaign, 1)
	})
	handler := NewApprovedActionHandler(newFakeRepository(campaign))

	// Запрос по устаревшей версии не выдаёт решение иной версии.
	if _, err := handler.Handle(context.Background(), ApprovedActionQuery{
		TenantID: approvalTenant, DraftRef: approvalDraft, DraftVersion: 1,
	}); !errors.Is(err, ErrDraftDecisionNotFound) {
		t.Fatalf("запрос устаревшей версии: ожидалась ErrDraftDecisionNotFound, получено %v", err)
	}
}

func TestApprovedActionDoesNotReturnCurrentApprovalForStaleVersion(t *testing.T) {
	// Регрессия дефекта: черновик доработан, по новой версии принято новое решение.
	// Запрос устаревшей версии не должен получить решение новой версии.
	campaign := campaignWithDecision(t, func(campaign *domain.Campaign) {
		approveCurrent(t, approvalDraft, "decision-1")(campaign)
		reviseDraft(t, campaign, 1)
		approveDraftVersion(t, approvalDraft, 2, "decision-2")(campaign)
	})
	handler := NewApprovedActionHandler(newFakeRepository(campaign))

	action, err := handler.Handle(context.Background(), ApprovedActionQuery{
		TenantID: approvalTenant, DraftRef: approvalDraft, DraftVersion: 2,
	})
	if err != nil {
		t.Fatalf("запрос текущей версии: %v", err)
	}
	if action.DraftVersion != 2 || action.DecisionID != "decision-2" {
		t.Fatalf("текущая версия: действие v%d решение %q", action.DraftVersion, action.DecisionID)
	}

	if _, err := handler.Handle(context.Background(), ApprovedActionQuery{
		TenantID: approvalTenant, DraftRef: approvalDraft, DraftVersion: 1,
	}); !errors.Is(err, ErrDraftDecisionNotFound) {
		t.Fatalf("запрос устаревшей версии получил чужое решение: %v", err)
	}
}

func TestApprovedActionDoesNotReturnDecisionForUnknownVersion(t *testing.T) {
	campaign := campaignWithDecision(t, approveCurrent(t, approvalDraft, "decision-1"))
	handler := NewApprovedActionHandler(newFakeRepository(campaign))

	if _, err := handler.Handle(context.Background(), ApprovedActionQuery{
		TenantID: approvalTenant, DraftRef: approvalDraft, DraftVersion: 2,
	}); !errors.Is(err, ErrDraftDecisionNotFound) {
		t.Fatalf("неизвестная версия: ожидалась ErrDraftDecisionNotFound, получено %v", err)
	}
}

func TestApprovedActionHidesUnknownAndForeignTenant(t *testing.T) {
	campaign := campaignWithDecision(t, approveCurrent(t, approvalDraft, "decision-1"))
	handler := NewApprovedActionHandler(newFakeRepository(campaign))

	cases := map[string]ApprovedActionQuery{
		"неизвестный черновик": {TenantID: approvalTenant, DraftRef: "other@v1", DraftVersion: 1},
		"чужой тенант":         {TenantID: "tenant-2", DraftRef: approvalDraft, DraftVersion: 1},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := handler.Handle(context.Background(), q); !errors.Is(err, ErrDraftDecisionNotFound) {
				t.Fatalf("ожидалась ErrDraftDecisionNotFound, получено %v", err)
			}
		})
	}
}

func TestApprovedActionRequiresTenant(t *testing.T) {
	handler := NewApprovedActionHandler(newFakeRepository())
	if _, err := handler.Handle(context.Background(), ApprovedActionQuery{DraftRef: approvalDraft, DraftVersion: 1}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("ожидалась ErrTenantRequired, получено %v", err)
	}
}

func mustChannel(t *testing.T) domain.ActionChannel {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	return channel
}

// reviseDraft дорабатывает черновик: повышает версию и возвращает на утверждение.
func reviseDraft(t *testing.T, campaign *domain.Campaign, expectedVersion int) {
	t.Helper()
	if _, err := campaign.ReviseDraft(approvalDraft, expectedVersion, domain.DraftSpec{
		Material:  domain.MaterialID("material-2"),
		Segment:   domain.SegmentID("segment-1"),
		Recipient: domain.RecipientID("recipient-1"),
		Channel:   mustChannel(t),
	}); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}
}

// approveDraftVersion фиксирует положительное решение по точной версии черновика.
func approveDraftVersion(t *testing.T, draftID domain.DraftID, version int, decisionID domain.DecisionID) func(*domain.Campaign) {
	t.Helper()
	return func(campaign *domain.Campaign) {
		if _, _, err := campaign.ApproveDraft(domain.DecisionParams{
			DecisionID:   decisionID,
			DraftID:      draftID,
			DraftVersion: version,
			Actor:        domain.ActorID("operator-1"),
			DecidedAt:    testNow(),
		}); err != nil {
			t.Fatalf("ApproveDraft v%d: %v", version, err)
		}
	}
}
