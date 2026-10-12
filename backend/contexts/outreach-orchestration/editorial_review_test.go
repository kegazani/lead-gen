package outreachorchestration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/inmemory"
	tappeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	"lead-gen/contracts/content-production/contentversion"
	contract "lead-gen/contracts/outreach-orchestration/editorialreview"
)

type versionSource struct{ contentversion.Source }

func (versionSource) Version(_ context.Context, q contentversion.Query) (contentversion.Version, error) {
	if q.TenantID != "tenant-1" || q.AssetID != "asset-1" || q.VersionID != "v1" {
		return contentversion.Version{}, contentversion.ErrNotFound
	}
	return contentversion.Version{AssetID: "asset-1", VersionID: "v1", Subject: "Exact subject", Body: "Exact body"}, nil
}

func TestEditorialDecisionIsIndependentCASAndExactRead(t *testing.T) {
	store := inmemory.NewEditorialStore()
	svc := tappeditorial.New(store, versionSource{}, func() time.Time { return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) })
	handoff := tappeditorial.Handoff{TenantID: "tenant-1", RequestID: "request-1", Items: []tappeditorial.Item{{LeadID: "lead-1", MaterialRef: "asset-1@v1", AssetID: "asset-1", VersionID: "v1"}}}
	campaign, repeated, err := svc.Submit(context.Background(), handoff)
	if err != nil || repeated || campaign.Purpose != "editorial" {
		t.Fatalf("Submit=%+v repeated=%v err=%v", campaign, repeated, err)
	}
	replayed, repeated, err := svc.Submit(context.Background(), handoff)
	if err != nil || !repeated || replayed.CampaignID != campaign.CampaignID {
		t.Fatalf("idempotent handoff replay=%+v repeated=%v err=%v", replayed, repeated, err)
	}
	changed := handoff
	changed.Items = append([]tappeditorial.Item(nil), handoff.Items...)
	changed.Items[0].VersionID = "other-version"
	changed.Items[0].MaterialRef = "asset-1@other-version"
	if _, _, err := svc.Submit(context.Background(), changed); err == nil {
		t.Fatal("same request key accepted a different editorial item set")
	}
	q := contract.Query{TenantID: "tenant-1", CampaignID: campaign.CampaignID, LeadID: "lead-1", MaterialRef: "asset-1@v1", VersionID: "v1"}
	read, err := svc.Read(context.Background(), q)
	if err != nil || read.Subject != "Exact subject" || read.Body != "Exact body" {
		t.Fatalf("exact read=%+v err=%v", read, err)
	}
	bad := q
	bad.TenantID = "other"
	if _, err := svc.Read(context.Background(), bad); err == nil {
		t.Fatal("чужой tenant прочитан")
	}
	decision := contract.Command{Query: q, ExpectedVersion: "v1", Outcome: "approved", Actor: "operator-1"}
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := svc.Decide(context.Background(), decision); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("идемпотентный конкурентный повтор: %v", err)
		}
	}
	read, err = svc.Read(context.Background(), q)
	if err != nil || read.Decision == nil || read.Decision.Outcome != "approved" || read.Decision.Actor != "operator-1" {
		t.Fatalf("decision не persisted: %+v %v", read, err)
	}
	events, err := svc.Events(context.Background(), campaign.CampaignID)
	if err != nil || len(events) != 1 || events[0].EventType != tappeditorial.EventTypeDecisionRecorded || events[0].Outcome != "approved" {
		t.Fatalf("editorial event log=%+v err=%v", events, err)
	}
	decision.Outcome = "rejected"
	decision.Reason = "нет"
	if _, err := svc.Decide(context.Background(), decision); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("CAS conflict ожидался, получено %v", err)
	}
	decision.ExpectedVersion = "stale"
	decision.VersionID = "stale"
	if _, err := svc.Decide(context.Background(), decision); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("stale POST должен отклоняться, получено %v", err)
	}
}
