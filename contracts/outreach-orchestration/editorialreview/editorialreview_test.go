package editorialreview

import (
	"context"
	"errors"
	"testing"
)

func TestInProcDelegatesExactVersion(t *testing.T) {
	port := InProc(InProcHandler{
		ReadFunc: func(_ context.Context, q Query) (Record, error) {
			return Record{Query: q, Subject: "Тема", Body: "Тело"}, nil
		},
		DecideFunc: func(_ context.Context, c Command) (Record, error) {
			if c.ExpectedVersion != c.VersionID {
				return Record{}, ErrConflict
			}
			return Record{Query: c.Query, Decision: &Decision{Outcome: c.Outcome, Actor: c.Actor, VersionID: c.VersionID, MaterialRef: c.MaterialRef}}, nil
		},
	})
	query := Query{TenantID: "t1", CampaignID: "c1", LeadID: "l1", MaterialRef: "a1@v1", VersionID: "v1"}
	read, err := port.Read(context.Background(), query)
	if err != nil || read.Subject != "Тема" || read.Body != "Тело" {
		t.Fatalf("Read: %+v, %v", read, err)
	}
	command := Command{Query: query, ExpectedVersion: "v0", Outcome: "approved", Actor: "operator"}
	if _, err := port.Decide(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale exact version: %v", err)
	}
}

func TestInProcUnavailableIsHonest(t *testing.T) {
	if _, err := InProc(InProcHandler{}).Read(context.Background(), Query{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась недоступность, получено %v", err)
	}
}
