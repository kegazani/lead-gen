package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	agents "lead-gen/backend/contexts/agent-orchestration"
	leads "lead-gen/backend/contexts/lead-management"
	"lead-gen/backend/platform/migrate"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

// Настоящая PostgreSQL-память: подтверждённый ICP и поздний контакт живут на
// разных источниках; отзыв первого снимает зачёт, сохраняя второй в dossier.
func TestResearchCriteriaPostgresLateContactAndRevocation(t *testing.T) {
	dsn := os.Getenv("LEADGEN_CRITERIA_TEST_DSN")
	if dsn == "" {
		t.Skip("нужна отдельная PostgreSQL БД для межконтекстной проверки критериев")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool, "../../contexts/lead-management/migrations/red", migrate.OwnSchema("lead-management")); err != nil {
		t.Fatal(err)
	}
	store := leads.NewPostgresResearchMemory(pool)
	scope := memory.Scope{TenantID: fmt.Sprintf("criteria-e2e-%d", time.Now().UnixNano()), Purpose: "lead-research"}
	now := time.Now().UTC().Truncate(time.Microsecond)
	fresh := now.Add(time.Hour)
	profileText := "Alpha Company\nCompany number 00000001\nAlpha Company manufactures building materials in United Kingdom."
	loc := func(text, quote string) memory.Locator {
		start := strings.Index(text, quote)
		return memory.Locator{Start: start, End: start + len(quote), Quote: quote}
	}
	quote := "Alpha Company manufactures building materials in United Kingdom."
	first := memory.SaveObservationRequest{Scope: scope, ObservationID: "qualified-profile", MissionID: "human-mission", ActionID: "profile-action", QueryAttemptID: "profile-attempt", Source: memory.Source{URL: "https://registry.example/00000001", Revision: "1", Representation: "text", Content: profileText, ObservedAt: now, Kind: "registry", Authority: "companies-house"}, Subject: memory.Subject{Key: "alpha", Name: "Alpha Company", Kind: "company", Country: "GB", Locator: loc(profileText, profileText)}, Identifiers: []memory.Identifier{{Kind: "company-number", Jurisdiction: "GB", Authority: "companies-house", Original: "00000001", Method: "registry-record", MethodVersion: "test-fixture-v1", Locator: loc(profileText, "00000001")}}}
	positive := agents.ResearchAssessment{ObservationID: first.ObservationID, CriteriaVersion: 1, Accepted: true, Reason: "criteria-evidence-accepted"}
	for _, id := range []string{"__human_icp", "__human_market"} {
		locator := loc(profileText, quote)
		first.Claims = append(first.Claims, memory.Claim{Predicate: "criteria-evidence:1:" + id + ":match", Value: quote, Original: quote, SubjectKey: "alpha", Locator: locator, Knowledge: "observed", Method: "llm-evidence", MethodVersion: "test-fixture-v1"})
		positive.EvidenceRefs = append(positive.EvidenceRefs, fmt.Sprintf("%s:1:%s:match:%d:%d", first.ObservationID, id, locator.Start, locator.End))
	}
	saved, err := store.SaveObservation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	missionScope := rm.Scope{TenantID: scope.TenantID, PurposeID: scope.Purpose}
	check := func(sets []agents.ResearchAssessment) agents.ResearchCriteriaSupport {
		t.Helper()
		got, err := agents.CheckResearchCriteriaSupport(ctx, store, missionScope, saved.EntityID, 1, sets)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := check([]agents.ResearchAssessment{positive}); !got.Accepted {
		t.Fatalf("первичная оценка: %#v", got)
	}
	contactText := "Alpha Company\nCompany number 00000001\nAlpha Company email Sales@example.org"
	late := first
	late.ObservationID = "late-contact"
	late.ActionID = "contact-action"
	late.QueryAttemptID = "contact-attempt"
	late.Source = memory.Source{URL: "https://alpha.example/contact", Revision: "1", Representation: "text", Content: contactText, ObservedAt: now, Kind: "page"}
	late.Subject.Locator = loc(contactText, contactText)
	late.Identifiers = append([]memory.Identifier(nil), first.Identifiers...)
	late.Identifiers[0].Method = "llm-observation"
	late.Identifiers[0].Locator = loc(contactText, "00000001")
	late.Claims = []memory.Claim{{Predicate: "email", Value: "Sales@example.org", Original: "Sales@example.org", SubjectKey: "alpha", Locator: loc(contactText, "Alpha Company email Sales@example.org"), Knowledge: "confirmed", Method: "subject-block", MethodVersion: "test-fixture-v1", FreshUntil: &fresh}}
	later, err := store.SaveObservation(ctx, late)
	if err != nil {
		t.Fatal(err)
	}
	if later.EntityID != saved.EntityID || !later.HasEmail {
		t.Fatalf("late contact не связан: %#v %#v", saved, later)
	}
	sets := []agents.ResearchAssessment{positive, {ObservationID: late.ObservationID, CriteriaVersion: 1, Accepted: false, Reason: "criteria-evidence-missing"}}
	if got := check(sets); !got.Accepted {
		t.Fatalf("unknown contactpage стёр положительный ICP: %#v", got)
	}
	forged := positive
	forged.ObservationID = "other-mission-observation"
	forged.EvidenceRefs = append([]string(nil), positive.EvidenceRefs...)
	for i := range forged.EvidenceRefs {
		forged.EvidenceRefs[i] = strings.ReplaceAll(forged.EvidenceRefs[i], positive.ObservationID, forged.ObservationID)
	}
	if got := check([]agents.ResearchAssessment{forged}); got.Accepted {
		t.Fatal("чужой observationID получил зачёт")
	}
	dossier, err := store.Get(ctx, memory.GetRequest{Scope: scope, EntityID: saved.EntityID})
	if err != nil {
		t.Fatal(err)
	}
	var profileSource string
	for _, source := range dossier.Sources {
		if source.URL == first.Source.URL {
			profileSource = source.ID
		}
	}
	if profileSource == "" {
		t.Fatal("нет сохранённого профиля")
	}
	if err := store.RevokeSource(ctx, memory.RevokeSourceRequest{Scope: scope, SourceID: profileSource, Reason: "test explicit withdrawal"}); err != nil {
		t.Fatal(err)
	}
	if got := check(sets); got.Accepted {
		t.Fatal("отозванное основание продолжает давать зачёт")
	}
	dossier, err = store.Get(ctx, memory.GetRequest{Scope: scope, EntityID: saved.EntityID})
	if err != nil {
		t.Fatal(err)
	}
	contactRetained := false
	for _, a := range dossier.Assertions {
		if a.Predicate == "email" && a.Access == "available" {
			contactRetained = true
		}
	}
	if !contactRetained {
		t.Fatal("отзыв старого профиля удалил отдельное позднее наблюдение контакта")
	}
	otherScope := rm.Scope{TenantID: scope.TenantID, PurposeID: "other-purpose"}
	got, err := agents.CheckResearchCriteriaSupport(ctx, store, otherScope, saved.EntityID, 1, sets)
	if err != nil || got.Accepted {
		t.Fatalf("cross-purpose: %#v %v", got, err)
	}
}
