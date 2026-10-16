package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	domain "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	contract "lead-gen/contracts/lead-management/researchmemory"
)

// Сравнение ограничено одинаковыми именами в разрешённом scope и 32 capture.
// Непроверенные кандидаты остаются сигналом и никогда не меняют canonical ID.
func recordResearchIdentityCandidates(ctx context.Context, tx pgx.Tx, q contract.SaveObservationRequest, entityID string) ([]string, error) {
	rows, e := tx.Query(ctx, `SELECT a.entity_id,a.assertion_id,s.snapshot FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=$1 AND a.purpose=$2 AND a.predicate='name' AND a.entity_id<>$3 AND s.revoked_at IS NULL AND lower(regexp_replace(trim(a.value),'\s+',' ','g'))=$4 ORDER BY a.recorded_at DESC,a.assertion_id LIMIT 32`, q.Scope.TenantID, q.Scope.Purpose, entityID, domain.IdentityName(q.Subject.Name))
	if e != nil {
		return nil, e
	}
	type peer struct {
		entity, assertion string
		source            contract.Source
	}
	peers := []peer{}
	for rows.Next() {
		var p peer
		var raw []byte
		if e = rows.Scan(&p.entity, &p.assertion, &raw); e != nil {
			rows.Close()
			return nil, e
		}
		if e = json.Unmarshal(raw, &p.source); e != nil {
			rows.Close()
			return nil, e
		}
		peers = append(peers, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	affected := []string{}
	ownAssertion := domain.StableID("ast_", q.Scope.TenantID, q.Scope.Purpose, q.ObservationID, "0")
	for _, p := range peers {
		if !domain.SameOrigin(p.source.URL, q.Source.URL) {
			continue
		}
		source, target := q.Source, p.source
		locator, ok := domain.NavigationCandidate(source, target.URL)
		if !ok {
			source, target = p.source, q.Source
			locator, ok = domain.NavigationCandidate(source, target.URL)
		}
		if !ok {
			continue
		}
		left, right := entityID, p.entity
		if left > right {
			left, right = right, left
		}
		id := domain.StableID("identity-candidate_", q.Scope.TenantID, q.Scope.Purpose, left, right, source.ID, target.ID)
		candidate := contract.IdentityCandidate{ID: id, LeftEntityID: left, RightEntityID: right, State: "pending", Signals: []string{"exact-name", "same-origin", "captured-navigation"}, SourceID: source.ID, TargetSourceID: target.ID, Navigation: locator, EvidenceAssertionIDs: []string{ownAssertion, p.assertion}, CreatedAt: time.Now().UTC()}
		tag, err := tx.Exec(ctx, `INSERT INTO "lead-management".research_identity_candidate(tenant_id,purpose,candidate_id,left_entity_id,right_entity_id,source_id,target_source_id,snapshot,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) ON CONFLICT DO NOTHING`, q.Scope.TenantID, q.Scope.Purpose, id, left, right, source.ID, target.ID, researchJSON(candidate), candidate.CreatedAt)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			affected = append(affected, left, right)
		}
	}
	return affected, nil
}
func readResearchIdentityCandidates(ctx context.Context, tx pgx.Tx, s contract.Scope, ids []string) ([]contract.IdentityCandidate, error) {
	rows, e := tx.Query(ctx, `SELECT c.snapshot,source.revoked_at IS NULL AND target.revoked_at IS NULL,l.canonical_entity_id,r.canonical_entity_id,EXISTS(SELECT 1 FROM "lead-management".research_identity_decision d JOIN "lead-management".research_identity_state state USING(tenant_id,purpose,decision_id) JOIN "lead-management".research_identity_member dl ON dl.tenant_id=d.tenant_id AND dl.purpose=d.purpose AND dl.entity_id=d.left_entity_id JOIN "lead-management".research_identity_member dr ON dr.tenant_id=d.tenant_id AND dr.purpose=d.purpose AND dr.entity_id=d.right_entity_id WHERE d.tenant_id=c.tenant_id AND d.purpose=c.purpose AND d.action='reject' AND state.state='active' AND ((dl.canonical_entity_id=l.canonical_entity_id AND dr.canonical_entity_id=r.canonical_entity_id) OR (dr.canonical_entity_id=l.canonical_entity_id AND dl.canonical_entity_id=r.canonical_entity_id))) FROM "lead-management".research_identity_candidate c JOIN "lead-management".research_source source ON source.tenant_id=c.tenant_id AND source.purpose=c.purpose AND source.source_id=c.source_id JOIN "lead-management".research_source target ON target.tenant_id=c.tenant_id AND target.purpose=c.purpose AND target.source_id=c.target_source_id JOIN "lead-management".research_identity_member l ON l.tenant_id=c.tenant_id AND l.purpose=c.purpose AND l.entity_id=c.left_entity_id JOIN "lead-management".research_identity_member r ON r.tenant_id=c.tenant_id AND r.purpose=c.purpose AND r.entity_id=c.right_entity_id WHERE c.tenant_id=$1 AND c.purpose=$2 AND (c.left_entity_id=ANY($3) OR c.right_entity_id=ANY($3)) ORDER BY c.created_at,c.candidate_id LIMIT 501`, s.TenantID, s.Purpose, ids)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contract.IdentityCandidate{}
	for rows.Next() {
		var raw []byte
		var available, rejected bool
		var left, right string
		if e = rows.Scan(&raw, &available, &left, &right, &rejected); e != nil {
			return nil, e
		}
		var c contract.IdentityCandidate
		if e = json.Unmarshal(raw, &c); e != nil {
			return nil, e
		}
		c.State = "pending"
		if !available {
			c.State = "unavailable"
			c.Navigation = contract.Locator{}
		} else if left == right {
			c.State = "linked"
		} else if rejected {
			c.State = "rejected"
		}
		out = append(out, c)
		if len(out) > 500 {
			return nil, fmt.Errorf("%w: превышен предел отображения кандидатов тождества", contract.ErrInvalid)
		}
	}
	return out, rows.Err()
}
func pendingResearchCandidateIDs(candidates []contract.IdentityCandidate) []string {
	ids := []string{}
	for _, c := range candidates {
		if c.State == "pending" {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
func readResearchIdentityGrounds(ctx context.Context, tx pgx.Tx, s contract.Scope, ids []string) ([]contract.IdentityGround, error) {
	rows, e := tx.Query(ctx, `SELECT DISTINCT i.kind,i.jurisdiction,i.authority,i.normalized FROM "lead-management".research_identifier i JOIN "lead-management".research_source source USING(tenant_id,purpose,source_id) WHERE i.tenant_id=$1 AND i.purpose=$2 AND i.entity_id=ANY($3) AND source.revoked_at IS NULL ORDER BY i.kind,i.jurisdiction,i.authority,i.normalized`, s.TenantID, s.Purpose, ids)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contract.IdentityGround{}
	for rows.Next() {
		var g contract.IdentityGround
		if e = rows.Scan(&g.Kind, &g.Jurisdiction, &g.Authority, &g.Value); e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func readResearchDistinctGroups(ctx context.Context, tx pgx.Tx, s contract.Scope, canonical string) ([]string, error) {
	rows, e := tx.Query(ctx, `SELECT DISTINCT CASE WHEN l.canonical_entity_id=$3 THEN r.canonical_entity_id ELSE l.canonical_entity_id END FROM "lead-management".research_identity_decision d JOIN "lead-management".research_identity_state state USING(tenant_id,purpose,decision_id) JOIN "lead-management".research_identity_member l ON l.tenant_id=d.tenant_id AND l.purpose=d.purpose AND l.entity_id=d.left_entity_id JOIN "lead-management".research_identity_member r ON r.tenant_id=d.tenant_id AND r.purpose=d.purpose AND r.entity_id=d.right_entity_id WHERE d.tenant_id=$1 AND d.purpose=$2 AND d.action='reject' AND state.state='active' AND (l.canonical_entity_id=$3 OR r.canonical_entity_id=$3) ORDER BY 1`, s.TenantID, s.Purpose, canonical)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
