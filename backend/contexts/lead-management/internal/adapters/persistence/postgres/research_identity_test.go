package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	contract "lead-gen/contracts/lead-management/researchmemory"
)

func identityPort(t *testing.T, p contract.Port) contract.IdentityPort {
	t.Helper()
	identity, ok := p.(contract.IdentityPort)
	if !ok {
		t.Fatal("production memory не реализует identity port")
	}
	return identity
}
func identityPair(t *testing.T, p contract.Port, withContact bool) (contract.SaveObservationRequest, contract.SaveObservationResult, contract.SaveObservationRequest, contract.SaveObservationResult) {
	t.Helper()
	a := researchInput(t, "main", false, false)
	a.Identifiers = nil
	a.Source.URL = "https://company.example.org/"
	a.Source.Family = "company.example.org"
	b := researchInput(t, "contacts", false, withContact)
	b.Identifiers = nil
	b.Source.URL = "https://company.example.org/contacts"
	b.Source.Family = "company.example.org"
	return a, saveResearch(t, p, a), b, saveResearch(t, p, b)
}
func identityRequest(t *testing.T, p contract.Port, scope contract.Scope, left, right, decision string) contract.IdentityDecisionRequest {
	t.Helper()
	identity := identityPort(t, p)
	q := contract.IdentityDecisionRequest{Scope: scope, DecisionID: decision, Action: "link", LeftEntityID: left, RightEntityID: right, Actor: "human-operator", Reason: "Проверены реквизиты и навигация собственных страниц компании", ExpectedRevisions: map[string]int64{}}
	for _, id := range []string{left, right} {
		g, e := identity.ResolveIdentity(context.Background(), contract.GetRequest{Scope: scope, EntityID: id})
		if e != nil {
			t.Fatal(e)
		}
		for member, rev := range g.MemberRevisions {
			q.ExpectedRevisions[member] = rev
		}
		d, e := p.Get(context.Background(), contract.GetRequest{Scope: scope, EntityID: id})
		if e != nil {
			t.Fatal(e)
		}
		for _, assertion := range d.Assertions {
			if assertion.EntityID == id && assertion.Predicate == "name" && assertion.Access == "available" {
				q.EvidenceAssertionIDs = append(q.EvidenceAssertionIDs, assertion.ID)
				break
			}
		}
	}
	return q
}
func revokeRequest(t *testing.T, identity contract.IdentityPort, scope contract.Scope, left, right, target, id string) contract.IdentityDecisionRequest {
	t.Helper()
	q := contract.IdentityDecisionRequest{Scope: scope, DecisionID: id, Action: "revoke", RevokeDecisionID: target, Actor: "human-operator", Reason: "Пересмотрено прежнее решение", ExpectedRevisions: map[string]int64{}}
	for _, id := range []string{left, right} {
		g, e := identity.ResolveIdentity(context.Background(), contract.GetRequest{Scope: scope, EntityID: id})
		if e != nil {
			t.Fatal(e)
		}
		for member, rev := range g.MemberRevisions {
			q.ExpectedRevisions[member] = rev
		}
	}
	return q
}
func TestResearchIdentityManualPagesBecomeOneReversibleCompany(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a, first, b, second := identityPair(t, p, true)
	if first.EntityID == second.EntityID {
		t.Fatal("domain/name сами объединили страницы")
	}
	list, e := p.List(ctx, contract.ListRequest{Scope: a.Scope})
	if e != nil || list.Total != 2 {
		t.Fatalf("кандидаты до решения: %+v %v", list, e)
	}
	q := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "manual-link")
	linked, e := identity.DecideIdentity(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if len(linked.Groups) != 1 || len(linked.Groups[0].MemberEntityIDs) != 2 || linked.Groups[0].CanonicalEntityID != first.EntityID {
		t.Fatalf("проекция: %+v", linked)
	}
	duplicate, e := identity.DecideIdentity(ctx, q)
	if e != nil || !duplicate.Duplicate || duplicate.Decision.ProjectionRevision != linked.Decision.ProjectionRevision {
		t.Fatalf("replay: %+v %v", duplicate, e)
	}
	// Новый адаптер не теряет решение, источники и авторство.
	p = researchPort(t)
	identity = identityPort(t, p)
	list, e = p.List(ctx, contract.ListRequest{Scope: a.Scope, Query: "Sales@example.org", MissionID: b.MissionID})
	if e != nil || list.Total != 1 || list.Entities[0].ID != first.EntityID || !list.Entities[0].HasEmail {
		t.Fatalf("canonical list: %+v %v", list, e)
	}
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Sources) != 2 || !d.Entity.HasEmail || len(d.IdentityDecisions) != 1 {
		t.Fatalf("combined dossier: %+v", d)
	}
	for _, n := range d.Needs {
		if n.KnowledgeState != "satisfied" {
			t.Fatalf("общая потребность не пересмотрена: %+v", n)
		}
	}
	batch, e := identity.ResolveIdentityBatch(ctx, contract.IdentityBatchRequest{Scope: a.Scope, EntityIDs: []string{first.EntityID, second.EntityID}})
	if e != nil || len(batch) != 2 || batch[0].CanonicalEntityID != batch[1].CanonicalEntityID || !batch[0].HasEmail {
		t.Fatalf("bulk: %+v %v", batch, e)
	}
	changes, e := identity.ReadIdentityChanges(ctx, contract.IdentityChangesRequest{Scope: a.Scope})
	if e != nil || len(changes) != 1 || len(changes[0].EntityIDs) != 2 {
		t.Fatalf("owner events: %+v %v", changes, e)
	}
	undo := revokeRequest(t, identity, a.Scope, first.EntityID, second.EntityID, q.DecisionID, "undo-link")
	split, e := identity.DecideIdentity(ctx, undo)
	if e != nil || len(split.Groups) != 2 {
		t.Fatalf("split: %+v %v", split, e)
	}
	list, e = p.List(ctx, contract.ListRequest{Scope: a.Scope})
	if e != nil || list.Total != 2 {
		t.Fatalf("исходные компании утрачены: %+v %v", list, e)
	}
	d, e = p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || d.Entity.HasEmail || len(d.Sources) != 1 {
		t.Fatalf("контакт остался на ошибочно связанном субъекте: %+v %v", d, e)
	}
	original := saveResearch(t, p, b)
	if original.EntityID != second.EntityID || original.CanonicalEntityID != second.EntityID {
		t.Fatalf("переписана source identity: %+v", original)
	}
}
func TestResearchIdentityNeedsCurrentVersionsAndLocalEvidence(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a, first, _, second := identityPair(t, p, false)
	base := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "link")
	missing := base
	missing.EvidenceAssertionIDs = missing.EvidenceAssertionIDs[:1]
	if _, e := identity.DecideIdentity(ctx, missing); !errors.Is(e, contract.ErrInvalid) {
		t.Fatalf("принято одностороннее основание: %v", e)
	}
	foreign := base
	foreign.Scope.Purpose = "other"
	if _, e := identity.DecideIdentity(ctx, foreign); !errors.Is(e, contract.ErrNotFound) {
		t.Fatalf("чужая область: %v", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"concurrent-a", "concurrent-b"} {
		q := base
		q.DecisionID = id
		wg.Go(func() { _, e := identity.DecideIdentity(ctx, q); errs <- e })
	}
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, contract.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("CAS: success=%d conflict=%d", success, conflicts)
	}
	if _, e := identity.DecideIdentity(ctx, base); !errors.Is(e, contract.ErrConflict) {
		t.Fatalf("устаревшая команда применена: %v", e)
	}
}
func TestResearchIdentityRejectAndStrongClusterVeto(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a := researchInput(t, "id-one", true, false)
	first := saveResearch(t, p, a)
	b := researchInput(t, "unresolved", false, false)
	b.Identifiers = nil
	second := saveResearch(t, p, b)
	q := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "negative")
	q.Action = "reject"
	rejected, e := identity.DecideIdentity(ctx, q)
	if e != nil || rejected.Decision.State != "active" {
		t.Fatalf("reject %+v %v", rejected, e)
	}
	link := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "blocked-link")
	if _, e := identity.DecideIdentity(ctx, link); !errors.Is(e, contract.ErrConflict) {
		t.Fatalf("отрицание проигнорировано: %v", e)
	}
	if _, e = identity.DecideIdentity(ctx, revokeRequest(t, identity, a.Scope, first.EntityID, second.EntityID, q.DecisionID, "undo-negative")); e != nil {
		t.Fatal(e)
	}
	link = identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "accepted-link")
	if _, e = identity.DecideIdentity(ctx, link); e != nil {
		t.Fatal(e)
	}
	c := researchInput(t, "id-two", true, false)
	c.Source.Content = strings.ReplaceAll(c.Source.Content, "00000001", "00000002")
	c.Subject.Locator.Quote = c.Source.Content
	c.Identifiers[0].Original = "00000002"
	c.Identifiers[0].Locator.Quote = "00000002"
	third := saveResearch(t, p, c)
	bridge := identityRequest(t, p, a.Scope, second.EntityID, third.EntityID, "bridge")
	if _, e = identity.DecideIdentity(ctx, bridge); !errors.Is(e, contract.ErrConflict) {
		t.Fatalf("транзитивный конфликт принят: %v", e)
	}
	list, e := p.List(ctx, contract.ListRequest{Scope: a.Scope})
	if e != nil || list.Total != 2 {
		t.Fatalf("cluster veto испортил группы: %+v %v", list, e)
	}
}
func TestResearchIdentityRevokedEvidenceSuspendsLinkAndAllowsMetadataUndo(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a, first, _, second := identityPair(t, p, true)
	q := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "manual-link")
	if _, e := identity.DecideIdentity(ctx, q); e != nil {
		t.Fatal(e)
	}
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	var source string
	for _, src := range d.Sources {
		if src.URL == a.Source.URL {
			source = src.ID
		}
	}
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: source, Reason: "отзыв основания"}); e != nil {
		t.Fatal(e)
	}
	history, e := identity.ListIdentityDecisions(ctx, contract.IdentityHistoryRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || len(history) != 1 || history[0].State != "unavailable" || history[0].Reason == q.Reason {
		t.Fatalf("история раскрыла/сохранила подтверждение: %+v %v", history, e)
	}
	ga, e := identity.ResolveIdentity(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || len(ga.MemberEntityIDs) != 1 || ga.MemberRevisions[first.EntityID] == 0 {
		t.Fatalf("metadata CAS недоступен: %+v %v", ga, e)
	}
	batch, e := identity.ResolveIdentityBatch(ctx, contract.IdentityBatchRequest{Scope: a.Scope, EntityIDs: []string{first.EntityID, second.EntityID}})
	if e != nil || batch[0].Available || !batch[1].Available {
		t.Fatalf("availability: %+v %v", batch, e)
	}
	if _, e = identity.DecideIdentity(ctx, revokeRequest(t, identity, a.Scope, first.EntityID, second.EntityID, q.DecisionID, "undo-unavailable")); e != nil {
		t.Fatal(e)
	}
}

func TestResearchIdentityAlternativeLinkRetainsGroupUntilAllProofsRevoked(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a, first, b, second := identityPair(t, p, true)
	// Вторая capture каждого субъекта даёт отдельные доступные основания.
	a2 := a
	a2.ObservationID = "main-second"
	a2.Source.Revision = "v2"
	saveResearch(t, p, a2)
	b2 := b
	b2.ObservationID = "contact-second"
	b2.Source.Revision = "v2"
	saveResearch(t, p, b2)
	refs := map[string]map[string]string{first.EntityID: {}, second.EntityID: {}}
	for id := range refs {
		d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: id})
		if e != nil {
			t.Fatal(e)
		}
		versions := map[string]string{}
		for _, s := range d.Sources {
			versions[s.ID] = s.Revision
		}
		for _, assertion := range d.Assertions {
			if assertion.Predicate == "name" {
				refs[id][versions[assertion.SourceID]] = assertion.ID
			}
		}
	}
	firstLink := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "link-first-capture")
	firstLink.EvidenceAssertionIDs = []string{refs[first.EntityID]["v1"], refs[second.EntityID]["v1"]}
	if _, e := identity.DecideIdentity(ctx, firstLink); e != nil {
		t.Fatal(e)
	}
	secondLink := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "link-second-capture")
	secondLink.EvidenceAssertionIDs = []string{refs[first.EntityID]["v2"], refs[second.EntityID]["v2"]}
	if _, e := identity.DecideIdentity(ctx, secondLink); e != nil {
		t.Fatal(e)
	}
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil {
		t.Fatal(e)
	}
	var sourceV1, sourceV2 string
	for _, s := range d.Sources {
		if s.URL == a.Source.URL {
			if s.Revision == "v1" {
				sourceV1 = s.ID
			} else {
				sourceV2 = s.ID
			}
		}
	}
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: sourceV1, Reason: "первая capture ошибочна"}); e != nil {
		t.Fatal(e)
	}
	g, e := identity.ResolveIdentity(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || len(g.MemberEntityIDs) != 2 || len(g.DecisionIDs) != 1 {
		t.Fatalf("альтернативный accepted путь потерян: %+v %v", g, e)
	}
	if e = p.RevokeSource(ctx, contract.RevokeSourceRequest{Scope: a.Scope, SourceID: sourceV2, Reason: "вторая capture тоже отозвана"}); e != nil {
		t.Fatal(e)
	}
	g, e = identity.ResolveIdentity(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || len(g.MemberEntityIDs) != 1 {
		t.Fatalf("необоснованное тождество сохранилось: %+v %v", g, e)
	}
}

func TestResearchIdentityNewStrongContradictionSuspendsOldManualLink(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a := researchInput(t, "registry-a", true, false)
	first := saveResearch(t, p, a)
	b := researchInput(t, "unresolved-b", false, true)
	b.Identifiers = nil
	second := saveResearch(t, p, b)
	q := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "manual-link")
	if _, e := identity.DecideIdentity(ctx, q); e != nil {
		t.Fatal(e)
	}
	// Изменяется знание B, не его исходный ID: поздний реестр выявил несовместимость.
	update := researchInput(t, "unresolved-b", true, false)
	update.ObservationID = "new-strong-id"
	update.Source.Revision = "registry-v2"
	update.Source.Content = strings.ReplaceAll(update.Source.Content, "00000001", "00000002")
	update.Subject.Locator.Quote = update.Source.Content
	update.Identifiers[0].Original = "00000002"
	update.Identifiers[0].Locator.Quote = "00000002"
	saved := saveResearch(t, p, update)
	if saved.EntityID != second.EntityID || saved.CanonicalEntityID != second.EntityID {
		t.Fatalf("contradiction projection: %+v", saved)
	}
	history, e := identity.ListIdentityDecisions(ctx, contract.IdentityHistoryRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || len(history) != 1 || history[0].State != "conflict" {
		t.Fatalf("историческое решение не пересмотрено: %+v %v", history, e)
	}
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || d.Entity.HasEmail {
		t.Fatalf("подтверждение контакта B осталось A: %+v %v", d, e)
	}
}

func TestResearchIdentityNavigationCandidateRequiresReviewOrGroundedDistinction(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a := researchInput(t, "homepage", false, false)
	a.Identifiers = nil
	a.Source.URL = "https://company.example.org/"
	a.Source.Content += "[Contacts](/contacts)\n"
	a.Subject.Locator.Quote = a.Source.Content
	a.Subject.Locator.End = len(a.Source.Content)
	b := researchInput(t, "contactpage", false, true)
	b.Identifiers = nil
	b.Source.URL = "https://company.example.org/contacts"
	first := saveResearch(t, p, a)
	second := saveResearch(t, p, b)
	if !second.IdentityReviewRequired || len(second.CandidateIDs) != 1 || second.CanonicalEntityID == first.EntityID {
		t.Fatalf("candidate не сохранён/слился: %+v", second)
	}
	batch, e := identity.ResolveIdentityBatch(ctx, contract.IdentityBatchRequest{Scope: a.Scope, EntityIDs: []string{first.EntityID, second.EntityID}})
	if e != nil {
		t.Fatal(e)
	}
	for _, state := range batch {
		if !state.IdentityReviewRequired || state.Name != "Alpha Company" || len(state.GroundedIdentifiers) != 0 {
			t.Fatalf("identity review projection: %+v", state)
		}
	}
	d, e := p.Get(ctx, contract.GetRequest{Scope: a.Scope, EntityID: first.EntityID})
	if e != nil || len(d.IdentityCandidates) != 1 || d.IdentityCandidates[0].State != "pending" || d.IdentityCandidates[0].Navigation.Quote != "[Contacts](/contacts)" {
		t.Fatalf("candidate evidence: %+v %v", d.IdentityCandidates, e)
	}
	reject := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "different-companies")
	reject.Action = "reject"
	reject.Reason = "Оператор сверил оба источника: это разные деловые субъекты"
	if _, e = identity.DecideIdentity(ctx, reject); e != nil {
		t.Fatal(e)
	}
	batch, e = identity.ResolveIdentityBatch(ctx, contract.IdentityBatchRequest{Scope: a.Scope, EntityIDs: []string{first.EntityID, second.EntityID}})
	if e != nil {
		t.Fatal(e)
	}
	for _, state := range batch {
		if state.IdentityReviewRequired || len(state.DistinctCanonicalEntityIDs) != 1 {
			t.Fatalf("manual distinct не применяется: %+v", state)
		}
	}
	// Отмена отрицательного решения снова требует проверки, не создавая merge.
	if _, e = identity.DecideIdentity(ctx, revokeRequest(t, identity, a.Scope, first.EntityID, second.EntityID, reject.DecisionID, "undo-reject")); e != nil {
		t.Fatal(e)
	}
	batch, e = identity.ResolveIdentityBatch(ctx, contract.IdentityBatchRequest{Scope: a.Scope, EntityIDs: []string{first.EntityID, second.EntityID}})
	if e != nil || !batch[0].IdentityReviewRequired || len(batch[0].DistinctCanonicalEntityIDs) != 0 {
		t.Fatalf("reopened review: %+v %v", batch, e)
	}
	link := identityRequest(t, p, a.Scope, first.EntityID, second.EntityID, "same-company")
	if _, e = identity.DecideIdentity(ctx, link); e != nil {
		t.Fatal(e)
	}
	list, e := p.List(ctx, contract.ListRequest{Scope: a.Scope})
	if e != nil || list.Total != 1 || list.Entities[0].IdentityReviewRequired {
		t.Fatalf("linked review state: %+v %v", list, e)
	}
}

func TestResearchIdentityBatchExposesOnlyScopedGroundedIdentifiers(t *testing.T) {
	ctx := context.Background()
	p := researchPort(t)
	identity := identityPort(t, p)
	a := researchInput(t, "registry", true, false)
	first := saveResearch(t, p, a)
	batch, e := identity.ResolveIdentityBatch(ctx, contract.IdentityBatchRequest{Scope: a.Scope, EntityIDs: []string{first.EntityID}})
	if e != nil || len(batch) != 1 || len(batch[0].GroundedIdentifiers) != 1 {
		t.Fatalf("grounded IDs: %+v %v", batch, e)
	}
	g := batch[0].GroundedIdentifiers[0]
	if g.Kind != "company-number" || g.Jurisdiction != "GB" || g.Authority != "companies-house" || g.Value != "00000001" {
		t.Fatalf("namespace потерян: %+v", g)
	}
	foreign := a.Scope
	foreign.Purpose = "other"
	if _, e = identity.ResolveIdentityBatch(ctx, contract.IdentityBatchRequest{Scope: foreign, EntityIDs: []string{first.EntityID}}); !errors.Is(e, contract.ErrNotFound) {
		t.Fatalf("чужой ID раскрыт: %v", e)
	}
}
