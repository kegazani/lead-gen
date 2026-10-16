package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	rm "lead-gen/contracts/agent-orchestration/researchmission"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

type criteriaMemoryDouble struct {
	memory.Port
	dossier memory.Dossier
	err     error
	last    memory.GetRequest
}

func (m *criteriaMemoryDouble) Get(_ context.Context, q memory.GetRequest) (memory.Dossier, error) {
	m.last = q
	return m.dossier, m.err
}

func criteriaFixture() (*criteriaMemoryDouble, Assessment) {
	quote := "Компания Альфа производит стройматериалы в России."
	source := memory.Source{ID: "source-positive", URL: "https://example.org/profile", Revision: "v1", Content: quote, ObservedAt: time.Now().Add(-time.Hour)}
	dossier := memory.Dossier{Entity: memory.Entity{ID: "entity", Revision: 1}, Sources: []memory.Source{source}}
	assessment := Assessment{ObservationID: "own-observation", CriteriaVersion: 1, Accepted: true, Reason: "criteria-evidence-accepted"}
	for _, id := range []string{"__human_icp", "__human_market"} {
		dossier.Assertions = append(dossier.Assertions, memory.Assertion{ID: "assertion-" + id, EntityID: "entity", ObservationID: assessment.ObservationID, SourceID: source.ID, Predicate: "criteria-evidence:1:" + id + ":match", Value: quote, Locator: memory.Locator{Start: 0, End: len(quote), Quote: quote}, Access: "available", SupportState: "satisfied", Knowledge: "observed", Freshness: "unknown"})
		assessment.EvidenceRefs = append(assessment.EvidenceRefs, fmt.Sprintf("%s:1:%s:match:0:%d", assessment.ObservationID, id, len(quote)))
	}
	return &criteriaMemoryDouble{dossier: dossier}, assessment
}

func TestCriteriaSupportPreservesCompletePositiveWhenNewContactHasUnknownCriteria(t *testing.T) {
	store, accepted := criteriaFixture()
	unknown := Assessment{ObservationID: "later-contact", CriteriaVersion: 1, Reason: "criteria-evidence-missing"}
	got, err := resolveCriteriaSupport(context.Background(), store, rm.Scope{TenantID: "tenant", PurposeID: "purpose"}, "entity", 1, []Assessment{accepted, unknown})
	if err != nil || !got.Accepted || len(got.EvidenceRefs) != 2 {
		t.Fatalf("положительные основания утрачены: %#v %v", got, err)
	}
	if store.last.Scope.TenantID != "tenant" || store.last.Scope.Purpose != "purpose" {
		t.Fatal("потерян scope")
	}
	got.EvidenceRefs[0] = "modified"
	if accepted.EvidenceRefs[0] == "modified" {
		t.Fatal("переписан immutable assessment")
	}
}

func TestCriteriaSupportCannotBorrowOtherMissionSameCriteriaVersion(t *testing.T) {
	store, accepted := criteriaFixture()
	for i := range store.dossier.Assertions {
		store.dossier.Assertions[i].ObservationID = "other-mission-observation"
	}
	got, err := resolveCriteriaSupport(context.Background(), store, rm.Scope{TenantID: "tenant", PurposeID: "purpose"}, "entity", 1, []Assessment{accepted})
	if err != nil || got.Accepted {
		t.Fatalf("чужая оценка стала основанием: %#v %v", got, err)
	}
}

func TestCriteriaSupportRechecksRevocationIdentitySupportAndValidity(t *testing.T) {
	for _, mode := range []string{"revoked", "unsupported", "stale", "refuted", "expired", "source-missing", "bytes-changed", "wrong-version", "incomplete-set"} {
		t.Run(mode, func(t *testing.T) {
			store, accepted := criteriaFixture()
			switch mode {
			case "revoked":
				store.dossier.Assertions[0].Access = "revoked"
			case "unsupported":
				store.dossier.Assertions[0].SupportState = "insufficient"
			case "stale":
				store.dossier.Assertions[0].Freshness = "stale"
			case "refuted":
				store.dossier.Assertions[0].Knowledge = "refuted"
			case "expired":
				past := time.Now().Add(-time.Minute)
				store.dossier.Assertions[0].ValidUntil = &past
			case "source-missing":
				store.dossier.Sources = nil
			case "bytes-changed":
				store.dossier.Sources[0].Content = "другой документ"
			case "wrong-version":
				accepted.CriteriaVersion = 2
			case "incomplete-set":
				accepted.EvidenceRefs = append(accepted.EvidenceRefs, "own-observation:1:missing:match:0:3")
			}
			got, err := resolveCriteriaSupport(context.Background(), store, rm.Scope{TenantID: "tenant", PurposeID: "purpose"}, "entity", 1, []Assessment{accepted})
			if err != nil || got.Accepted {
				t.Fatalf("непригодное основание зачтено: %#v %v", got, err)
			}
		})
	}
}

func TestCurrentExplicitNegativeVetoesOldPositive(t *testing.T) {
	store, positive := criteriaFixture()
	negative := positive
	negative.ObservationID = "negative-observation"
	negative.Accepted = false
	negative.Reason = "criteria-not-satisfied"
	negative.EvidenceRefs = nil
	for i, a := range store.dossier.Assertions {
		copy := a
		copy.ID += "-negative"
		copy.ObservationID = negative.ObservationID
		verdict := "match"
		if i == 0 {
			verdict = "mismatch"
			copy.Predicate = strings.TrimSuffix(copy.Predicate, ":match") + ":mismatch"
		}
		store.dossier.Assertions = append(store.dossier.Assertions, copy)
		negative.EvidenceRefs = append(negative.EvidenceRefs, fmt.Sprintf("%s:1:%s:%s:0:%d", negative.ObservationID, a.Predicate[len("criteria-evidence:1:"):len(a.Predicate)-len(":match")], verdict, len(a.Locator.Quote)))
	}
	for _, sets := range [][]Assessment{{positive, negative}, {negative, positive}} {
		got, err := resolveCriteriaSupport(context.Background(), store, rm.Scope{TenantID: "tenant", PurposeID: "purpose"}, "entity", 1, sets)
		if err != nil || got.Accepted || got.Reason != "criteria-review-required" {
			t.Fatalf("противоречие утрачено: %#v %v", got, err)
		}
	}
	for i := range store.dossier.Assertions {
		if store.dossier.Assertions[i].ObservationID == negative.ObservationID {
			store.dossier.Assertions[i].Access = "revoked"
		}
	}
	got, err := resolveCriteriaSupport(context.Background(), store, rm.Scope{TenantID: "tenant", PurposeID: "purpose"}, "entity", 1, []Assessment{positive, negative})
	if err != nil || !got.Accepted {
		t.Fatalf("отозванное противоречие продолжает veto: %#v %v", got, err)
	}
}

func TestCriteriaSupportUnavailableAndStorageErrorsAreDistinct(t *testing.T) {
	store, a := criteriaFixture()
	scope := rm.Scope{TenantID: "tenant", PurposeID: "purpose"}
	store.err = memory.ErrRevoked
	got, err := resolveCriteriaSupport(context.Background(), store, scope, "entity", 1, []Assessment{a})
	if err != nil || got.Accepted || got.Reason != "criteria-evidence-unavailable" {
		t.Fatalf("revoked: %#v %v", got, err)
	}
	store.err = errors.New("database disconnected")
	if _, err := resolveCriteriaSupport(context.Background(), store, scope, "entity", 1, []Assessment{a}); err == nil {
		t.Fatal("ошибка БД замаскирована какunknown")
	}
}

func TestCriteriaRefParserPreservesColonInCriterionIDAndRejectsForgedBounds(t *testing.T) {
	ref, ok := parseCriteriaEvidenceRef("obs:7:custom:criterion:match:12:40")
	if !ok || ref.criterionID != "custom:criterion" || ref.version != 7 {
		t.Fatalf("%#v %v", ref, ok)
	}
	for _, raw := range []string{"obs:1:id:match:-1:4", "obs:1:id:match:4:4", "obs:1:id:unknown:0:4", "borrowed", "obs:99999999999999999999999:id:match:0:4"} {
		if _, ok := parseCriteriaEvidenceRef(raw); ok {
			t.Fatalf("принята некорректная ссылка %q", raw)
		}
	}
}
