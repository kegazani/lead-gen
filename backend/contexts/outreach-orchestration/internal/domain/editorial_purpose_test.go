package domain

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEditorialCampaignCannotEnterDeliveryApproval(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	window, err := NewCampaignWindow(now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	campaign, err := CreateCampaign(CreateCampaignParams{ID: "editorial-campaign", TenantID: "tenant-1", Name: "Editorial", Window: window, Purpose: CampaignPurposeEditorial})
	if err != nil {
		t.Fatal(err)
	}
	if campaign.Purpose() != CampaignPurposeEditorial {
		t.Fatalf("purpose=%q", campaign.Purpose())
	}
	if _, _, err := campaign.DraftAction("draft-1", DraftSpec{Material: "asset@version", Segment: "lead-1", Recipient: "", Channel: mustChannel(t, ActionChannelEmailName)}, now); !errors.Is(err, ErrEditorialDeliveryForbidden) {
		t.Fatalf("delivery draft path allowed: %v", err)
	}
	if _, _, err := campaign.ApproveBatch(context.Background(), BatchApprovalParams{}, nil); !errors.Is(err, ErrEditorialDeliveryForbidden) {
		t.Fatalf("batch approval path allowed: %v", err)
	}
	if got := campaign.DomainEvents(); len(got) != 0 {
		t.Fatalf("editorial guard emitted delivery/domain events: %#v", got)
	}
}

func TestLegacyCampaignPurposeDefaultsToDelivery(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	window, err := NewCampaignWindow(now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	campaign, err := CreateCampaign(CreateCampaignParams{ID: "legacy-campaign", TenantID: "tenant-1", Name: "Legacy", Window: window})
	if err != nil {
		t.Fatal(err)
	}
	if campaign.Purpose() != CampaignPurposeDelivery {
		t.Fatalf("legacy default purpose=%q", campaign.Purpose())
	}
}
