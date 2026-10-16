package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	domain "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	contract "lead-gen/contracts/lead-management/researchmemory"
)

var _ contract.IdentityPort = (*ResearchMemoryRepository)(nil)

func ensureResearchIdentity(ctx context.Context, tx pgx.Tx, s contract.Scope) error {
	_, e := tx.Exec(ctx, `INSERT INTO "lead-management".research_identity_scope(tenant_id,purpose,revision) VALUES($1,$2,0) ON CONFLICT DO NOTHING`, s.TenantID, s.Purpose)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_identity_member(tenant_id,purpose,entity_id,canonical_entity_id,revision) SELECT e.tenant_id,e.purpose,e.entity_id,e.entity_id,s.revision FROM "lead-management".research_entity e JOIN "lead-management".research_identity_scope s USING(tenant_id,purpose) WHERE e.tenant_id=$1 AND e.purpose=$2 ON CONFLICT DO NOTHING`, s.TenantID, s.Purpose)
	return e
}
func loadResearchIdentityGraph(ctx context.Context, tx pgx.Tx, s contract.Scope) ([]domain.IdentityNode, []contract.IdentityDecision, map[string]bool, error) {
	nodes := []domain.IdentityNode{}
	positions := map[string]int{}
	rows, e := tx.Query(ctx, `SELECT entity_id,created_at FROM "lead-management".research_entity WHERE tenant_id=$1 AND purpose=$2 ORDER BY created_at,entity_id`, s.TenantID, s.Purpose)
	if e != nil {
		return nil, nil, nil, e
	}
	for rows.Next() {
		var n domain.IdentityNode
		if e = rows.Scan(&n.ID, &n.CreatedAt); e != nil {
			rows.Close()
			return nil, nil, nil, e
		}
		n.Identifiers = map[string]string{}
		positions[n.ID] = len(nodes)
		nodes = append(nodes, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, nil, e
	}
	rows, e = tx.Query(ctx, `SELECT i.entity_id,i.kind,i.jurisdiction,i.authority,i.normalized FROM "lead-management".research_identifier i JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE i.tenant_id=$1 AND i.purpose=$2 AND s.revoked_at IS NULL`, s.TenantID, s.Purpose)
	if e != nil {
		return nil, nil, nil, e
	}
	for rows.Next() {
		var id, kind, jurisdiction, authority, value string
		if e = rows.Scan(&id, &kind, &jurisdiction, &authority, &value); e != nil {
			rows.Close()
			return nil, nil, nil, e
		}
		n := &nodes[positions[id]]
		n.LegalJurisdictions = append(n.LegalJurisdictions, jurisdiction)
		namespace := domain.Hash([]string{jurisdiction, authority, kind})
		if old, ok := n.Identifiers[namespace]; ok && old != value {
			n.Identifiers[namespace] = "conflict:" + old + ":" + value
		} else {
			n.Identifiers[namespace] = value
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, nil, e
	}
	decisions := []contract.IdentityDecision{}
	available := map[string]bool{}
	rows, e = tx.Query(ctx, `SELECT d.snapshot,NOT EXISTS(SELECT 1 FROM "lead-management".research_identity_evidence ie JOIN "lead-management".research_assertion a USING(tenant_id,purpose,assertion_id) JOIN "lead-management".research_source s ON s.tenant_id=a.tenant_id AND s.purpose=a.purpose AND s.source_id=a.source_id WHERE ie.tenant_id=d.tenant_id AND ie.purpose=d.purpose AND ie.decision_id=d.decision_id AND s.revoked_at IS NOT NULL) FROM "lead-management".research_identity_decision d WHERE d.tenant_id=$1 AND d.purpose=$2 ORDER BY d.recorded_at,d.decision_id`, s.TenantID, s.Purpose)
	if e != nil {
		return nil, nil, nil, e
	}
	for rows.Next() {
		var raw []byte
		var ok bool
		if e = rows.Scan(&raw, &ok); e != nil {
			rows.Close()
			return nil, nil, nil, e
		}
		var d contract.IdentityDecision
		if e = json.Unmarshal(raw, &d); e != nil {
			rows.Close()
			return nil, nil, nil, e
		}
		decisions = append(decisions, d)
		available[d.ID] = ok
	}
	e = rows.Err()
	rows.Close()
	return nodes, decisions, available, e
}

// rebuildResearchIdentity фиксирует текущую проекцию и событие одной транзакцией.
// Consumers читают последовательность revision; старые команды проверяют CAS.
func rebuildResearchIdentity(ctx context.Context, tx pgx.Tx, s contract.Scope, cause string, force bool, forcedAffected ...string) error {
	if e := ensureResearchIdentity(ctx, tx, s); e != nil {
		return e
	}
	nodes, decisions, available, e := loadResearchIdentityGraph(ctx, tx, s)
	if e != nil {
		return e
	}
	projection := domain.ProjectIdentity(nodes, decisions, available)
	old := map[string]string{}
	states := map[string]string{}
	rows, e := tx.Query(ctx, `SELECT entity_id,canonical_entity_id FROM "lead-management".research_identity_member WHERE tenant_id=$1 AND purpose=$2`, s.TenantID, s.Purpose)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, canonical string
		if e = rows.Scan(&id, &canonical); e != nil {
			rows.Close()
			return e
		}
		old[id] = canonical
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = tx.Query(ctx, `SELECT decision_id,state FROM "lead-management".research_identity_state WHERE tenant_id=$1 AND purpose=$2`, s.TenantID, s.Purpose)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, state string
		if e = rows.Scan(&id, &state); e != nil {
			rows.Close()
			return e
		}
		states[id] = state
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	changed := force
	affected := map[string]bool{}
	for _, id := range forcedAffected {
		affected[id] = true
	}
	for id, canonical := range projection.Canonical {
		if old[id] != canonical {
			changed = true
			affected[id] = true
			affected[old[id]] = true
			affected[canonical] = true
		}
	}
	for _, d := range decisions {
		if states[d.ID] != projection.States[d.ID] {
			changed = true
			affected[d.LeftEntityID] = true
			affected[d.RightEntityID] = true
		}
	}
	if !changed {
		return nil
	}
	var revision int64
	e = tx.QueryRow(ctx, `UPDATE "lead-management".research_identity_scope SET revision=revision+1 WHERE tenant_id=$1 AND purpose=$2 RETURNING revision`, s.TenantID, s.Purpose).Scan(&revision)
	if e != nil {
		return e
	}
	for id, canonical := range projection.Canonical {
		_, e = tx.Exec(ctx, `UPDATE "lead-management".research_identity_member SET canonical_entity_id=$4,revision=$5 WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3`, s.TenantID, s.Purpose, id, canonical, revision)
		if e != nil {
			return e
		}
	}
	for id, state := range projection.States {
		_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_identity_state(tenant_id,purpose,decision_id,state,revision) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,purpose,decision_id) DO UPDATE SET state=EXCLUDED.state,revision=EXCLUDED.revision`, s.TenantID, s.Purpose, id, state, revision)
		if e != nil {
			return e
		}
	}
	ids := []string{}
	for id := range affected {
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	// Версии всех затронутых исходных сущностей защищают от устаревших manual commands.
	if len(ids) > 0 {
		_, e = tx.Exec(ctx, `UPDATE "lead-management".research_entity SET revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND purpose=$2 AND entity_id=ANY($3)`, s.TenantID, s.Purpose, ids)
		if e != nil {
			return e
		}
	}
	for _, id := range ids {
		if e = refreshResearchNeeds(ctx, tx, s, id); e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_identity_change(tenant_id,purpose,revision,event_id,cause,entity_ids,created_at) VALUES($1,$2,$3,$4,$5,$6,now())`, s.TenantID, s.Purpose, revision, domain.StableID("identity-event_", s.TenantID, s.Purpose, fmt.Sprint(revision)), cause, ids)
	return e
}
func readResearchIdentityGroup(ctx context.Context, tx pgx.Tx, s contract.Scope, id string) (contract.IdentityGroup, error) {
	group := contract.IdentityGroup{MemberEntityIDs: []string{}, DecisionIDs: []string{}, MemberRevisions: map[string]int64{}}
	e := tx.QueryRow(ctx, `SELECT m.canonical_entity_id,s.revision FROM "lead-management".research_identity_member m JOIN "lead-management".research_identity_scope s USING(tenant_id,purpose) WHERE m.tenant_id=$1 AND m.purpose=$2 AND m.entity_id=$3`, s.TenantID, s.Purpose, id).Scan(&group.CanonicalEntityID, &group.ProjectionRevision)
	if errors.Is(e, pgx.ErrNoRows) {
		return group, contract.ErrNotFound
	}
	if e != nil {
		return group, e
	}
	rows, e := tx.Query(ctx, `SELECT m.entity_id,e.revision FROM "lead-management".research_identity_member m JOIN "lead-management".research_entity e USING(tenant_id,purpose,entity_id) WHERE m.tenant_id=$1 AND m.purpose=$2 AND m.canonical_entity_id=$3 ORDER BY m.entity_id`, s.TenantID, s.Purpose, group.CanonicalEntityID)
	if e != nil {
		return group, e
	}
	for rows.Next() {
		var id string
		var revision int64
		if e = rows.Scan(&id, &revision); e != nil {
			rows.Close()
			return group, e
		}
		group.MemberEntityIDs = append(group.MemberEntityIDs, id)
		group.MemberRevisions[id] = revision
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return group, e
	}
	rows, e = tx.Query(ctx, `SELECT d.decision_id FROM "lead-management".research_identity_decision d JOIN "lead-management".research_identity_state s USING(tenant_id,purpose,decision_id) WHERE d.tenant_id=$1 AND d.purpose=$2 AND d.action='link' AND s.state='active' AND d.left_entity_id=ANY($3) AND d.right_entity_id=ANY($3) ORDER BY d.recorded_at,d.decision_id`, s.TenantID, s.Purpose, group.MemberEntityIDs)
	if e != nil {
		return group, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return group, e
		}
		group.DecisionIDs = append(group.DecisionIDs, id)
	}
	e = rows.Err()
	rows.Close()
	return group, e
}
func (r *ResearchMemoryRepository) ResolveIdentity(ctx context.Context, q contract.GetRequest) (contract.IdentityGroup, error) {
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return contract.IdentityGroup{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = readResearchIdentityEntity(ctx, tx, q.Scope, q.EntityID); e != nil {
		return contract.IdentityGroup{}, e
	}
	if e = ensureResearchIdentity(ctx, tx, q.Scope); e != nil {
		return contract.IdentityGroup{}, e
	}
	g, e := readResearchIdentityGroup(ctx, tx, q.Scope, q.EntityID)
	if e != nil {
		return g, e
	}
	return g, tx.Commit(ctx)
}
func readResearchIdentityHistory(ctx context.Context, tx pgx.Tx, s contract.Scope, ids []string, limit int) ([]contract.IdentityDecision, error) {
	rows, e := tx.Query(ctx, `SELECT d.snapshot,p.state,p.revision,NOT EXISTS(SELECT 1 FROM "lead-management".research_identity_evidence ie JOIN "lead-management".research_assertion a USING(tenant_id,purpose,assertion_id) JOIN "lead-management".research_source source ON source.tenant_id=a.tenant_id AND source.purpose=a.purpose AND source.source_id=a.source_id WHERE ie.tenant_id=d.tenant_id AND ie.purpose=d.purpose AND (ie.decision_id=d.decision_id OR ie.decision_id=d.revoke_decision_id) AND source.revoked_at IS NOT NULL) FROM "lead-management".research_identity_decision d JOIN "lead-management".research_identity_state p USING(tenant_id,purpose,decision_id) WHERE d.tenant_id=$1 AND d.purpose=$2 AND (cardinality($3::text[])=0 OR d.left_entity_id=ANY($3) OR d.right_entity_id=ANY($3)) ORDER BY d.recorded_at DESC,d.decision_id LIMIT $4`, s.TenantID, s.Purpose, ids, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contract.IdentityDecision{}
	for rows.Next() {
		var raw []byte
		var state string
		var revision int64
		var evidenceAvailable bool
		if e = rows.Scan(&raw, &state, &revision, &evidenceAvailable); e != nil {
			return nil, e
		}
		var d contract.IdentityDecision
		if e = json.Unmarshal(raw, &d); e != nil {
			return nil, e
		}
		d.State = state
		if !evidenceAvailable {
			d.Reason = "Основание недоступно"
		}
		d.ProjectionRevision = revision
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *ResearchMemoryRepository) ListIdentityDecisions(ctx context.Context, q contract.IdentityHistoryRequest) ([]contract.IdentityDecision, error) {
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	ids := []string{}
	if q.EntityID != "" {
		if _, e = readResearchIdentityEntity(ctx, tx, q.Scope, q.EntityID); e != nil {
			return nil, e
		}
		ids = append(ids, q.EntityID)
	}
	out, e := readResearchIdentityHistory(ctx, tx, q.Scope, ids, q.Limit)
	if e != nil {
		return nil, e
	}
	return out, tx.Commit(ctx)
}
func (r *ResearchMemoryRepository) DecideIdentity(ctx context.Context, q contract.IdentityDecisionRequest) (contract.IdentityDecisionResult, error) {
	var result contract.IdentityDecisionResult
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return result, e
	}
	defer tx.Rollback(ctx)
	if e = ensureResearchIdentity(ctx, tx, q.Scope); e != nil {
		return result, e
	}
	hash := domain.Hash(q)
	var oldHash string
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT payload_hash,snapshot FROM "lead-management".research_identity_decision WHERE tenant_id=$1 AND purpose=$2 AND decision_id=$3`, q.Scope.TenantID, q.Scope.Purpose, q.DecisionID).Scan(&oldHash, &raw)
	if e == nil {
		if oldHash != hash {
			return result, contract.ErrConflict
		}
		if e = json.Unmarshal(raw, &result.Decision); e != nil {
			return result, e
		}
		result.Duplicate = true
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return result, e
	} else {
		left, right := q.LeftEntityID, q.RightEntityID
		if q.Action == "revoke" {
			var prior contract.IdentityDecision
			var state string
			e = tx.QueryRow(ctx, `SELECT d.snapshot,s.state FROM "lead-management".research_identity_decision d JOIN "lead-management".research_identity_state s USING(tenant_id,purpose,decision_id) WHERE d.tenant_id=$1 AND d.purpose=$2 AND d.decision_id=$3`, q.Scope.TenantID, q.Scope.Purpose, q.RevokeDecisionID).Scan(&raw, &state)
			if errors.Is(e, pgx.ErrNoRows) {
				return result, contract.ErrNotFound
			}
			if e != nil {
				return result, e
			}
			if e = json.Unmarshal(raw, &prior); e != nil {
				return result, e
			}
			if prior.Action == "revoke" || state == "revoked" {
				return result, contract.ErrConflict
			}
			left, right = prior.LeftEntityID, prior.RightEntityID
		}
		a, e := readResearchIdentityEntity(ctx, tx, q.Scope, left)
		if e != nil {
			return result, e
		}
		b, e := readResearchIdentityEntity(ctx, tx, q.Scope, right)
		if e != nil {
			return result, e
		}
		if q.Action == "link" && (!researchCompatibleKinds(a.Kind, b.Kind)) {
			return result, fmt.Errorf("%w: разные типы субъектов", contract.ErrInvalid)
		}
		ga, e := readResearchIdentityGroup(ctx, tx, q.Scope, left)
		if e != nil {
			return result, e
		}
		gb, e := readResearchIdentityGroup(ctx, tx, q.Scope, right)
		if e != nil {
			return result, e
		}
		union := map[string]bool{}
		for _, ids := range [][]string{ga.MemberEntityIDs, gb.MemberEntityIDs} {
			for _, id := range ids {
				union[id] = true
			}
		}
		if len(q.ExpectedRevisions) != len(union) {
			return result, fmt.Errorf("%w: версии всех участников обязательны", contract.ErrConflict)
		}
		for id := range union {
			entity, e := readResearchIdentityEntity(ctx, tx, q.Scope, id)
			if e != nil {
				return result, e
			}
			if q.ExpectedRevisions[id] != entity.Revision {
				return result, contract.ErrConflict
			}
		}
		if q.Action == "reject" && ga.CanonicalEntityID == gb.CanonicalEntityID {
			return result, fmt.Errorf("%w: сначала отмените действующие связи", contract.ErrConflict)
		}
		if q.Action == "link" || q.Action == "reject" {
			endpoints := map[string]bool{}
			for _, id := range q.EvidenceAssertionIDs {
				var entity string
				var available bool
				e = tx.QueryRow(ctx, `SELECT a.entity_id,s.revoked_at IS NULL FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=$1 AND a.purpose=$2 AND a.assertion_id=$3`, q.Scope.TenantID, q.Scope.Purpose, id).Scan(&entity, &available)
				if errors.Is(e, pgx.ErrNoRows) {
					return result, contract.ErrNotFound
				}
				if e != nil {
					return result, e
				}
				if !available {
					return result, contract.ErrRevoked
				}
				if entity != left && entity != right {
					return result, fmt.Errorf("%w: основание другого субъекта", contract.ErrInvalid)
				}
				endpoints[entity] = true
			}
			if !endpoints[left] || !endpoints[right] {
				return result, fmt.Errorf("%w: нужны основания обоих субъектов", contract.ErrInvalid)
			}
		}
		decision := contract.IdentityDecision{ID: q.DecisionID, Action: q.Action, LeftEntityID: left, RightEntityID: right, ExpectedRevisions: q.ExpectedRevisions, Actor: q.Actor, Reason: q.Reason, EvidenceAssertionIDs: q.EvidenceAssertionIDs, RevokeDecisionID: q.RevokeDecisionID, Method: "manual-review", MethodVersion: "1", RecordedAt: time.Now().UTC()}
		// Проверка будущей проекции до записи исключает cluster-wide сильный конфликт.
		if q.Action == "link" {
			nodes, decisions, available, e := loadResearchIdentityGraph(ctx, tx, q.Scope)
			if e != nil {
				return result, e
			}
			decisions = append(decisions, decision)
			available[decision.ID] = true
			projected := domain.ProjectIdentity(nodes, decisions, available)
			if projected.States[decision.ID] != "active" {
				return result, fmt.Errorf("%w: несовместимые идентификаторы или действующее отрицание", contract.ErrConflict)
			}
		}
		_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_identity_decision(tenant_id,purpose,decision_id,action,left_entity_id,right_entity_id,revoke_decision_id,payload_hash,snapshot,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10)`, q.Scope.TenantID, q.Scope.Purpose, decision.ID, decision.Action, left, right, decision.RevokeDecisionID, hash, researchJSON(decision), decision.RecordedAt)
		if e != nil {
			return result, e
		}
		for _, id := range q.EvidenceAssertionIDs {
			_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_identity_evidence(tenant_id,purpose,decision_id,assertion_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, q.Scope.TenantID, q.Scope.Purpose, decision.ID, id)
			if e != nil {
				return result, e
			}
		}
		if e = rebuildResearchIdentity(ctx, tx, q.Scope, "decision:"+decision.ID, true); e != nil {
			return result, e
		}
		result.Decision = decision
	}
	e = tx.QueryRow(ctx, `SELECT state,revision FROM "lead-management".research_identity_state WHERE tenant_id=$1 AND purpose=$2 AND decision_id=$3`, q.Scope.TenantID, q.Scope.Purpose, result.Decision.ID).Scan(&result.Decision.State, &result.Decision.ProjectionRevision)
	if e != nil {
		return result, e
	}
	var evidenceAvailable bool
	e = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM "lead-management".research_identity_evidence ie JOIN "lead-management".research_assertion a USING(tenant_id,purpose,assertion_id) JOIN "lead-management".research_source source ON source.tenant_id=a.tenant_id AND source.purpose=a.purpose AND source.source_id=a.source_id WHERE ie.tenant_id=$1 AND ie.purpose=$2 AND (ie.decision_id=$3 OR ie.decision_id=$4) AND source.revoked_at IS NOT NULL)`, q.Scope.TenantID, q.Scope.Purpose, result.Decision.ID, result.Decision.RevokeDecisionID).Scan(&evidenceAvailable)
	if e != nil {
		return result, e
	}
	if !evidenceAvailable {
		result.Decision.Reason = "Основание недоступно"
	}
	groups := map[string]bool{}
	for _, id := range []string{result.Decision.LeftEntityID, result.Decision.RightEntityID} {
		g, e := readResearchIdentityGroup(ctx, tx, q.Scope, id)
		if e != nil {
			return result, e
		}
		if !groups[g.CanonicalEntityID] {
			groups[g.CanonicalEntityID] = true
			result.Groups = append(result.Groups, g)
		}
	}
	return result, tx.Commit(ctx)
}
func researchCompatibleKinds(a, b string) bool {
	if a == "discovery-hit" || b == "discovery-hit" {
		return false
	}
	if a == b {
		return true
	}
	return (a == "company" || a == "legal-entity") && (b == "company" || b == "legal-entity")
}

func (r *ResearchMemoryRepository) ResolveIdentityBatch(ctx context.Context, q contract.IdentityBatchRequest) ([]contract.IdentityEntityState, error) {
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = ensureResearchIdentity(ctx, tx, q.Scope); e != nil {
		return nil, e
	}
	out := []contract.IdentityEntityState{}
	groupFlags := map[string][2]bool{}
	for _, id := range q.EntityIDs {
		entity, e := readResearchEntity(ctx, tx, q.Scope, id)
		if errors.Is(e, contract.ErrRevoked) {
			meta, readErr := readResearchIdentityEntity(ctx, tx, q.Scope, id)
			if readErr != nil {
				return nil, readErr
			}
			group, readErr := readResearchIdentityGroup(ctx, tx, q.Scope, id)
			if readErr != nil {
				return nil, readErr
			}
			out = append(out, contract.IdentityEntityState{EntityID: id, EntityRevision: meta.Revision, CanonicalEntityID: group.CanonicalEntityID, IdentityRevision: group.ProjectionRevision, Available: false})
			continue
		}
		if e != nil {
			return nil, e
		}
		group, e := readResearchIdentityGroup(ctx, tx, q.Scope, id)
		if e != nil {
			return nil, e
		}
		flags, ok := groupFlags[group.CanonicalEntityID]
		if !ok {
			for _, member := range group.MemberEntityIDs {
				n, e := readResearchEntity(ctx, tx, q.Scope, member)
				if errors.Is(e, contract.ErrRevoked) {
					continue
				}
				if e != nil {
					return nil, e
				}
				flags[0] = flags[0] || n.HasContact
				flags[1] = flags[1] || n.HasEmail
			}
			groupFlags[group.CanonicalEntityID] = flags
		}
		grounds, e := readResearchIdentityGrounds(ctx, tx, q.Scope, group.MemberEntityIDs)
		if e != nil {
			return nil, e
		}
		distinct, e := readResearchDistinctGroups(ctx, tx, q.Scope, group.CanonicalEntityID)
		if e != nil {
			return nil, e
		}
		candidates, e := readResearchIdentityCandidates(ctx, tx, q.Scope, group.MemberEntityIDs)
		if e != nil {
			return nil, e
		}
		pending := pendingResearchCandidateIDs(candidates)
		out = append(out, contract.IdentityEntityState{EntityID: id, EntityRevision: entity.Revision, CanonicalEntityID: group.CanonicalEntityID, IdentityRevision: group.ProjectionRevision, HasContact: flags[0], HasEmail: flags[1], Available: true, Name: entity.Name, GroundedIdentifiers: grounds, DistinctCanonicalEntityIDs: distinct, IdentityReviewRequired: len(pending) > 0, CandidateIDs: pending})
	}
	return out, tx.Commit(ctx)
}
func (r *ResearchMemoryRepository) ReadIdentityChanges(ctx context.Context, q contract.IdentityChangesRequest) ([]contract.IdentityChange, error) {
	if e := domain.ValidateScope(q.Scope); e != nil {
		return nil, e
	}
	rows, e := r.pool.Query(ctx, `SELECT revision,event_id,cause,entity_ids,created_at FROM "lead-management".research_identity_change WHERE tenant_id=$1 AND purpose=$2 AND revision>$3 ORDER BY revision LIMIT $4`, q.Scope.TenantID, q.Scope.Purpose, q.AfterRevision, q.Limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contract.IdentityChange{}
	for rows.Next() {
		var c contract.IdentityChange
		if e = rows.Scan(&c.Revision, &c.EventID, &c.Cause, &c.EntityIDs, &c.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Метаданные CAS доступны владельцу scope даже после отзыва raw source: без них
// невозможно отменить ошибочное историческое решение.
func readResearchIdentityEntity(ctx context.Context, tx pgx.Tx, s contract.Scope, id string) (contract.Entity, error) {
	var entity contract.Entity
	e := tx.QueryRow(ctx, `SELECT entity_id,kind,country,revision,created_at,updated_at FROM "lead-management".research_entity WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3`, s.TenantID, s.Purpose, id).Scan(&entity.ID, &entity.Kind, &entity.Country, &entity.Revision, &entity.CreatedAt, &entity.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return entity, contract.ErrNotFound
	}
	return entity, e
}
