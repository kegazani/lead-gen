package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

const (
	testTenant   = domain.TenantID("tenant-1")
	testOperator = domain.ActorID("operator-1")
)

func TestApprovalQueueReturnsOnlyPendingDrafts(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewApprovalQueueHandler(newFakeRepository(campaign), allowAccess())

	items, err := handler.Handle(context.Background(), ApprovalQueue{
		CampaignIDs: []domain.CampaignID{campaign.ID()},
		TenantID:    testTenant,
		Actor:       testOperator,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("позиций очереди: got %d, want 1", len(items))
	}
	item := items[0]
	if item.DraftID != domain.DraftID("draft-pending") {
		t.Fatalf("в очереди не тот черновик: %q", item.DraftID)
	}
	if item.DraftVersion != 1 {
		t.Fatalf("версия черновика: got %d, want 1", item.DraftVersion)
	}
	if item.TenantID != domain.TenantID("tenant-1") {
		t.Fatalf("тенант: got %q", item.TenantID)
	}
	if item.Channel.String() != domain.ActionChannelCallName {
		t.Fatalf("канал: got %q", item.Channel)
	}
}

func TestApprovalQueueFiltersByTenant(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewApprovalQueueHandler(newFakeRepository(campaign), allowAccess())

	items, err := handler.Handle(context.Background(), ApprovalQueue{
		CampaignIDs: []domain.CampaignID{campaign.ID()},
		TenantID:    domain.TenantID("tenant-2"),
		Actor:       testOperator,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("кампания другого тенанта не попадает в очередь, got %d", len(items))
	}
}

func TestApprovalQueueRequiresTenant(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewApprovalQueueHandler(newFakeRepository(campaign), allowAccess())

	_, err := handler.Handle(context.Background(), ApprovalQueue{
		CampaignIDs: []domain.CampaignID{campaign.ID()},
		Actor:       testOperator,
	})
	if !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("запрос без тенанта: получено %v, ожидалась ErrTenantRequired", err)
	}
}

func TestApprovalQueueRequiresActor(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewApprovalQueueHandler(newFakeRepository(campaign), allowAccess())

	_, err := handler.Handle(context.Background(), ApprovalQueue{
		CampaignIDs: []domain.CampaignID{campaign.ID()},
		TenantID:    testTenant,
	})
	if !errors.Is(err, ErrActorRequired) {
		t.Fatalf("запрос без актора: получено %v, ожидалась ErrActorRequired", err)
	}
}

func TestApprovalQueueDeniesActorWithoutRight(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewApprovalQueueHandler(newFakeRepository(campaign), &stubAccess{allowed: false})

	_, err := handler.Handle(context.Background(), ApprovalQueue{
		CampaignIDs: []domain.CampaignID{campaign.ID()},
		TenantID:    testTenant,
		Actor:       testOperator,
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("актор без права: получено %v, ожидалась ErrAccessDenied", err)
	}
}

func TestApprovalQueueDeniesWithoutAccessPort(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewApprovalQueueHandler(newFakeRepository(campaign))

	_, err := handler.Handle(context.Background(), ApprovalQueue{
		CampaignIDs: []domain.CampaignID{campaign.ID()},
		TenantID:    testTenant,
		Actor:       testOperator,
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("без порта доступа: получено %v, ожидалась ErrAccessDenied", err)
	}
}
