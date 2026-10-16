package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/postgres"
	app "lead-gen/backend/contexts/lead-management/internal/app/researchmemory"
	contract "lead-gen/contracts/lead-management/researchmemory"
)

var researchReset sync.Once

func researchPort(t *testing.T) contract.Port {
	p := testPool(t)
	researchReset.Do(func() {
		_, e := p.Exec(context.Background(), `TRUNCATE "lead-management".research_entity,"lead-management".research_source,"lead-management".research_attempt,"lead-management".research_identity_scope,"lead-management".research_identity_change CASCADE`)
		if e != nil {
			t.Fatal(e)
		}
	})
	return app.New(postgres.NewResearchMemoryRepository(p))
}
func researchInput(t *testing.T, suffix string, registry, contact bool) contract.SaveObservationRequest {
	t.Helper()
	body := "Alpha Company\nCompany number 00000001\n"
	if contact {
		body += "Email Sales@example.org\n"
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	fresh := now.Add(time.Hour)
	loc := func(s string) contract.Locator {
		n := strings.Index(body, s)
		return contract.Locator{Start: n, End: n + len(s), Quote: s}
	}
	q := contract.SaveObservationRequest{Scope: contract.Scope{TenantID: t.Name(), Purpose: "research"}, ObservationID: "obs-" + suffix, MissionID: "mission-" + suffix, ActionID: "action-" + suffix, QueryAttemptID: "attempt-" + suffix, Source: contract.Source{URL: "https://" + suffix + ".example.org/company", Revision: "v1", Representation: "markdown", Content: body, ObservedAt: now, Kind: "page", Family: "origin-" + suffix}, Subject: contract.Subject{Key: "company-1", Name: "Alpha Company", Kind: "company", Country: "GB", Locator: loc(body)}, Identifiers: []contract.Identifier{{Kind: "company-number", Jurisdiction: "GB", Authority: "companies-house", Original: "00000001", Method: "source-block", MethodVersion: "1", Locator: loc("00000001")}}}
	if registry {
		q.Source.Kind = "registry"
		q.Source.Authority = "companies-house"
		q.Identifiers[0].Method = "registry-record"
	}
	if contact {
		q.Claims = []contract.Claim{{Predicate: "email", Value: "Sales@example.org", SubjectKey: q.Subject.Key, Locator: loc("Sales@example.org"), Knowledge: "confirmed", Method: "subject-block", MethodVersion: "1", FreshUntil: &fresh}}
	}
	return q
}
func saveResearch(t *testing.T, p contract.Port, q contract.SaveObservationRequest) contract.SaveObservationResult {
	t.Helper()
	r, e := p.SaveObservation(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestResearchMemoryPersistsLateContactWithTenantPurposeIsolation(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	a := researchInput(t, "registry", true, false)
	first := saveResearch(t, p, a)
	if !first.Created || !first.IdentityConfirmed || first.HasContact {
		t.Fatalf("неполный субъект не сохранён: %+v", first)
	}
	// Новый экземпляр адаптера читает тот же реальный persistent state.
	p = researchPort(t)
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	if d.Entity.Name != "Alpha Company" || len(d.Needs) != 2 || d.Needs[0].KnowledgeState != "open" || d.Needs[0].ExecutionState != "dormant" {
		t.Fatalf("неполная компания/потребность: %+v", d)
	}
	b := researchInput(t, "directory", false, true)
	second := saveResearch(t, p, b)
	if second.EntityID != first.EntityID || second.Created || !second.HasEmail || !second.IdentityConfirmed {
		t.Fatalf("поздний контакт не связан: first=%+v second=%+v", first, second)
	}
	duplicate := saveResearch(t, p, b)
	if !duplicate.Duplicate || duplicate.EntityID != second.EntityID || duplicate.Revision != second.Revision {
		t.Fatalf("повтор изменил результат: %+v", duplicate)
	}
	d, e = p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Sources) != 2 || !d.Entity.HasEmail || d.Needs[0].KnowledgeState != "satisfied" || d.Needs[0].ExecutionState != "dormant" {
		t.Fatalf("dossier/need не обновлены: %+v", d)
	}
	for _, scope := range []contract.Scope{{TenantID: "other", Purpose: a.Scope.Purpose}, {TenantID: a.Scope.TenantID, Purpose: "other"}} {
		if _, e = p.Get(ctx, contract.GetRequest{Scope: scope, EntityID: first.EntityID}); !errors.Is(e, contract.ErrNotFound) {
			t.Fatalf("утечка чужой области %v", e)
		}
		list, e := p.List(ctx, contract.ListRequest{Scope: scope, Query: "Alpha"})
		if e != nil || list.Total != 0 {
			t.Fatalf("retrieval чужой области: %+v %v", list, e)
		}
	}
	other := a
	other.Scope.TenantID = "other-" + t.Name()
	otherResult := saveResearch(t, p, other)
	if otherResult.EntityID == first.EntityID {
		t.Fatal("ID пересёк tenant")
	}
	list, e := p.List(ctx, contract.ListRequest{Scope: a.Scope, MissionID: b.MissionID, Query: "00000001"})
	if e != nil || list.Total != 1 || len(list.Entities) != 1 {
		t.Fatalf("scoped exact/fts retrieval: %+v %v", list, e)
	}
	if e = p.SetNeedExecution(ctx, contract.NeedExecutionRequest{Scope: a.Scope, EntityID: first.EntityID, MissionID: a.MissionID, Attach: true}); e != nil {
		t.Fatal(e)
	}
	if e = p.SetNeedExecution(ctx, contract.NeedExecutionRequest{Scope: a.Scope, EntityID: first.EntityID, MissionID: a.MissionID, Attach: false}); e != nil {
		t.Fatal(e)
	}
}
func TestResearchMemoryNeverMergesOnlyNameDomainOrUnverifiedID(t *testing.T) {
	p := researchPort(t)
	a := researchInput(t, "one", false, false)
	b := researchInput(t, "two", false, false)
	first := saveResearch(t, p, a)
	second := saveResearch(t, p, b)
	if first.EntityID == second.EntityID || first.IdentityConfirmed || second.IdentityConfirmed {
		t.Fatalf("неподтверждённое тождество: %+v %+v", first, second)
	}
	// Другой субъект на той же странице — самостоятельный кандидат.
	c := a
	c.ObservationID = "obs-other-subject"
	c.Subject.Key = "company-2"
	third := saveResearch(t, p, c)
	if third.EntityID == first.EntityID {
		t.Fatal("общий домен/страница объединили субъектов")
	}
}
func TestResearchMemoryRevocationRemovesRetrievalAndReopensNeed(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	a := researchInput(t, "registry", true, false)
	first := saveResearch(t, p, a)
	b := researchInput(t, "contact", false, true)
	saveResearch(t, p, b)
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	var sourceID string
	for _, s := range d.Sources {
		if s.URL == b.Source.URL {
			sourceID = s.ID
		}
	}
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: sourceID, Reason: "ошибочное основание"}); e != nil {
		t.Fatal(e)
	}
	d, e = p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	if d.Entity.HasEmail || d.Needs[0].KnowledgeState != "open" || len(d.Sources) != 1 {
		t.Fatalf("отзыв не пересмотрен: %+v", d)
	}
	for _, a := range d.Assertions {
		if a.SourceID == sourceID && (a.Value != "" || a.Original != "" || a.Locator.Quote != "" || a.Access != "revoked") {
			t.Fatalf("утечка отозванного фрагмента: %+v", a)
		}
	}
	list, e := p.List(ctx, contract.ListRequest{Scope: a.Scope, Query: "Sales@example.org"})
	if e != nil || list.Total != 0 {
		t.Fatalf("индекс раскрыл отозванные данные: %+v %v", list, e)
	}
	if _, e = p.SaveObservation(ctx, b); !errors.Is(e, contract.ErrRevoked) {
		t.Fatalf("late worker восстановил отзыв: %v", e)
	}
	// Полный отзыв не раскрывает даже название из запрещённого источника.
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: d.Sources[0].ID, Reason: "отзыв всех оснований"}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID}); !errors.Is(e, contract.ErrRevoked) {
		t.Fatalf("полностью отозванная сущность доступна: %v", e)
	}
}
func TestResearchMemoryIdempotencyConflictsAndConcurrentReplay(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	a := researchInput(t, "registry", true, false)
	var wg sync.WaitGroup
	results := make(chan contract.SaveObservationResult, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { r, e := p.SaveObservation(ctx, a); results <- r; errs <- e })
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	created := 0
	for r := range results {
		if id != "" && id != r.EntityID {
			t.Fatal("replay дал две компании")
		}
		id = r.EntityID
		if !r.Duplicate {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("реальных записей %d", created)
	}
	collision := a
	collision.ActionID = "different"
	if _, e := p.SaveObservation(ctx, collision); !errors.Is(e, contract.ErrConflict) {
		t.Fatalf("key collision не отклонён: %v", e)
	}
	changed := a
	changed.ObservationID = "changed"
	changed.Source.Content += "new material"
	changed.Subject.Locator.Quote = changed.Source.Content
	changed.Subject.Locator.End = len(changed.Source.Content)
	if _, e := p.SaveObservation(ctx, changed); !errors.Is(e, contract.ErrConflict) {
		t.Fatalf("immutable source изменился: %v", e)
	}
}
func TestResearchAttemptRecallScopedByVersionLocaleAndTTL(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	now := time.Now().UTC()
	scope := contract.Scope{TenantID: t.Name(), Purpose: "research"}
	a := contract.Attempt{Scope: scope, ID: "one", MissionID: "mission-a", ActionID: "action", Question: "  Find   Email ", SourceURL: "https://example.org", SourceRevision: "v1", Locale: "ru-RU", Tool: "firecrawl", Outcome: "no-match-on-version", ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	if e := p.SaveAttempt(ctx, a); e != nil {
		t.Fatal(e)
	}
	query := contract.RecallAttemptsRequest{Scope: scope, Question: "find email", SourceURL: a.SourceURL, SourceRevision: "v1", Locale: "ru-RU"}
	out, e := p.RecallAttempts(ctx, query)
	if e != nil || len(out) != 1 {
		t.Fatalf("память между миссиями: %+v %v", out, e)
	}
	for _, locale := range []string{"de-DE", "en-GB"} {
		query.Locale = locale
		out, e = p.RecallAttempts(ctx, query)
		if e != nil || len(out) != 0 {
			t.Fatalf("другой язык ограничен отрицанием: %+v %v", out, e)
		}
	}
	query.Locale = "ru-RU"
	query.SourceRevision = "v2"
	out, e = p.RecallAttempts(ctx, query)
	if e != nil || len(out) != 0 {
		t.Fatalf("новая версия ограничена отрицанием: %+v %v", out, e)
	}
	expired := a
	expired.ID = "expired"
	expired.SourceRevision = "v0"
	expired.ObservedAt = now.Add(-2 * time.Hour)
	expired.ExpiresAt = now.Add(-time.Hour)
	if e = p.SaveAttempt(ctx, expired); e != nil {
		t.Fatal(e)
	}
	query.SourceRevision = "v0"
	out, e = p.RecallAttempts(ctx, query)
	if e != nil || len(out) != 0 {
		t.Fatalf("истёкший TTL: %+v %v", out, e)
	}
	invalid := a
	invalid.ID = "global"
	invalid.SourceRevision = ""
	if e = p.SaveAttempt(ctx, invalid); !errors.Is(e, contract.ErrInvalid) {
		t.Fatalf("глобальное отрицание принято: %v", e)
	}
}

func TestResearchMemoryIdentitySupportSurvivesAlternativeButNotSoleRevocation(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	a := researchInput(t, "registry-a", true, false)
	first := saveResearch(t, p, a)
	b := researchInput(t, "contact-b", false, true)
	saveResearch(t, p, b)
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	var sourceA string
	for _, s := range d.Sources {
		if s.URL == a.Source.URL {
			sourceA = s.ID
		}
	}
	c := researchInput(t, "registry-c", true, false)
	third := saveResearch(t, p, c)
	if third.EntityID != first.EntityID {
		t.Fatal("альтернативный идентификатор не связан")
	}
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: sourceA, Reason: "отозвано основание A"}); e != nil {
		t.Fatal(e)
	}
	d, e = p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || !d.Entity.HasEmail {
		t.Fatalf("независимое основание C потеряно: %+v %v", d, e)
	}
	var sourceC string
	for _, s := range d.Sources {
		if s.URL == c.Source.URL {
			sourceC = s.ID
		}
	}
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: sourceC, Reason: "отозвано последнее основание"}); e != nil {
		t.Fatal(e)
	}
	d, e = p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	if d.Entity.HasEmail || d.Needs[0].KnowledgeState != "open" {
		t.Fatalf("контакт B остался подтверждённым без identity основания: %+v", d)
	}
	for _, a := range d.Assertions {
		if a.Predicate == "email" && (a.Knowledge != "confirmed" || a.Access != "available" || a.SupportState != "insufficient") {
			t.Fatalf("история/актуальное основание смешаны: %+v", a)
		}
	}
	replay := saveResearch(t, p, b)
	if replay.HasEmail || replay.IdentityConfirmed {
		t.Fatalf("запоздалый replay возвратил отозванную готовность: %+v", replay)
	}
	version := d.Entity.Revision
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: sourceC, Reason: "повтор"}); e != nil {
		t.Fatal(e)
	}
	d, e = p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || d.Entity.Revision != version {
		t.Fatalf("повтор отзыва изменил версию: %+v %v", d.Entity, e)
	}
}

func TestResearchMemoryIncompatibleIdentifiersNeverTransitivelyMerge(t *testing.T) {
	p := researchPort(t)
	a := researchInput(t, "registry-a", true, false)
	first := saveResearch(t, p, a)
	b := researchInput(t, "registry-b", true, false)
	b.Source.Content = strings.ReplaceAll(b.Source.Content, "00000001", "00000002")
	b.Subject.Locator.Quote = b.Source.Content
	b.Identifiers[0].Original = "00000002"
	b.Identifiers[0].Locator.Quote = "00000002"
	second := saveResearch(t, p, b)
	c := researchInput(t, "bridge", false, false)
	c.Source.Content += "Also number 00000002\n"
	c.Subject.Locator.End = len(c.Source.Content)
	c.Subject.Locator.Quote = c.Source.Content
	other := c.Identifiers[0]
	other.Original = "00000002"
	other.Locator.Start = strings.Index(c.Source.Content, other.Original)
	other.Locator.End = other.Locator.Start + len(other.Original)
	other.Locator.Quote = other.Original
	c.Identifiers = append(c.Identifiers, other)
	result := saveResearch(t, p, c)
	if !result.IdentityConflict || result.IdentityConfirmed || result.EntityID == first.EntityID || result.EntityID == second.EntityID {
		t.Fatalf("A~B~C объединили несовместимые IDs: %+v", result)
	}
}

func TestResearchMemoryUnknownAndStaleFreshnessNeverEraseHistoricalConfirmation(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	a := researchInput(t, "unknown", true, true)
	a.Claims[0].FreshUntil = nil
	result := saveResearch(t, p, a)
	if result.HasEmail {
		t.Fatal("неизвестная актуальность выдана за текущую")
	}
	b := researchInput(t, "stale", false, true)
	old := time.Now().Add(-48 * time.Hour).UTC()
	until := old.Add(time.Hour)
	b.Source.ObservedAt = old
	b.Claims[0].FreshUntil = &until
	saveResearch(t, p, b)
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: result.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	if d.Entity.HasEmail {
		t.Fatal("устаревший контакт credited")
	}
	states := map[string]bool{}
	for _, a := range d.Assertions {
		if a.Predicate == "email" {
			if a.Knowledge != "confirmed" {
				t.Fatal("устаревание уничтожило историческое подтверждение")
			}
			states[a.Freshness] = true
		}
	}
	if !states["unknown"] || !states["stale"] {
		t.Fatalf("состояния смешаны: %+v", states)
	}
}
