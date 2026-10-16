package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	domain "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	contract "lead-gen/contracts/lead-management/researchmemory"
)

// ResearchMemoryRepository хранит источники и утверждения в реальной красной БД.
// Локальная блокировка scope упорядочивает запись, отзыв и точное сопоставление;
// внешние вызовы не выполняются внутри транзакции.
type ResearchMemoryRepository struct{ pool *pgxpool.Pool }

func NewResearchMemoryRepository(p *pgxpool.Pool) *ResearchMemoryRepository {
	if p == nil {
		panic("researchmemory: nil red pool")
	}
	return &ResearchMemoryRepository{pool: p}
}

var _ contract.Port = (*ResearchMemoryRepository)(nil)

func (r *ResearchMemoryRepository) begin(ctx context.Context, s contract.Scope) (pgx.Tx, error) {
	if e := domain.ValidateScope(s); e != nil {
		return nil, e
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, domain.Hash(s)); e != nil {
		_ = tx.Rollback(ctx)
		return nil, e
	}
	return tx, nil
}
func researchJSON(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func identifierKey(i contract.Identifier) string {
	return domain.Hash([]string{i.Jurisdiction, i.Authority, i.Kind, i.Normalized})
}

func (r *ResearchMemoryRepository) SaveObservation(ctx context.Context, request contract.SaveObservationRequest) (contract.SaveObservationResult, error) {
	q, e := domain.NormalizeObservation(request)
	if e != nil {
		return contract.SaveObservationResult{}, e
	}
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return contract.SaveObservationResult{}, e
	}
	defer tx.Rollback(ctx)
	result, e := r.saveObservation(ctx, tx, q)
	if e != nil {
		return result, e
	}
	candidateAffected, e := recordResearchIdentityCandidates(ctx, tx, q, result.EntityID)
	if e != nil {
		return contract.SaveObservationResult{}, e
	}
	if e = rebuildResearchIdentity(ctx, tx, q.Scope, "observation:"+q.ObservationID, len(candidateAffected) > 0, candidateAffected...); e != nil {
		return contract.SaveObservationResult{}, e
	}
	group, e := readResearchIdentityGroup(ctx, tx, q.Scope, result.EntityID)
	if e != nil {
		return contract.SaveObservationResult{}, e
	}
	candidates, e := readResearchIdentityCandidates(ctx, tx, q.Scope, group.MemberEntityIDs)
	if e != nil {
		return contract.SaveObservationResult{}, e
	}
	result.CandidateIDs = pendingResearchCandidateIDs(candidates)
	result.IdentityReviewRequired = len(result.CandidateIDs) > 0
	result.CanonicalEntityID = group.CanonicalEntityID
	result.IdentityRevision = group.ProjectionRevision
	current, e := readResearchEntity(ctx, tx, q.Scope, result.EntityID)
	if e != nil {
		return contract.SaveObservationResult{}, e
	}
	result.Revision = current.Revision
	result.HasContact = false
	result.HasEmail = false
	for _, id := range group.MemberEntityIDs {
		member, e := readResearchEntity(ctx, tx, q.Scope, id)
		if errors.Is(e, contract.ErrRevoked) {
			continue
		}
		if e != nil {
			return contract.SaveObservationResult{}, e
		}
		result.HasContact = result.HasContact || member.HasContact
		result.HasEmail = result.HasEmail || member.HasEmail
	}
	if e = tx.Commit(ctx); e != nil {
		return contract.SaveObservationResult{}, e
	}
	return result, nil
}
func (r *ResearchMemoryRepository) saveObservation(ctx context.Context, tx pgx.Tx, q contract.SaveObservationRequest) (contract.SaveObservationResult, error) {
	var result contract.SaveObservationResult
	tenant, purpose := q.Scope.TenantID, q.Scope.Purpose
	hash := domain.Hash(q)
	var oldHash string
	var oldResult []byte
	var revoked bool
	e := tx.QueryRow(ctx, `SELECT o.payload_hash,o.result,s.revoked_at IS NOT NULL FROM "lead-management".research_observation o JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE o.tenant_id=$1 AND o.purpose=$2 AND o.observation_id=$3`, tenant, purpose, q.ObservationID).Scan(&oldHash, &oldResult, &revoked)
	if e == nil {
		if oldHash != hash {
			return result, contract.ErrConflict
		}
		if revoked {
			return result, contract.ErrRevoked
		}
		if e = json.Unmarshal(oldResult, &result); e != nil {
			return result, e
		}
		result.Duplicate = true
		current, readErr := readResearchEntity(ctx, tx, q.Scope, result.EntityID)
		if readErr != nil {
			return result, readErr
		}
		result.Revision = current.Revision
		result.HasEmail = current.HasEmail
		result.HasContact = current.HasContact
		result.IdentityConfirmed = false
		if !result.IdentityConflict {
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "lead-management".research_identifier i JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE i.tenant_id=$1 AND i.purpose=$2 AND i.entity_id=$3 AND s.revoked_at IS NULL)`, tenant, purpose, result.EntityID).Scan(&result.IdentityConfirmed)
			if e != nil {
				return result, e
			}
		}
		return result, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return result, e
	}
	var oldSourceID, oldContentHash string
	var oldSourceJSON []byte
	e = tx.QueryRow(ctx, `SELECT source_id,content_hash,revoked_at IS NOT NULL,snapshot FROM "lead-management".research_source WHERE tenant_id=$1 AND purpose=$2 AND source_url=$3 AND source_revision=$4`, tenant, purpose, q.Source.URL, q.Source.Revision).Scan(&oldSourceID, &oldContentHash, &revoked, &oldSourceJSON)
	if e == nil {
		if revoked {
			return result, contract.ErrRevoked
		}
		if oldContentHash != domain.ContentHash(q.Source.Content) {
			return result, contract.ErrConflict
		}
		q.Source.ID = oldSourceID
		var oldSource contract.Source
		if e = json.Unmarshal(oldSourceJSON, &oldSource); e != nil {
			return result, e
		}
		if domain.Hash(oldSource) != domain.Hash(q.Source) {
			return result, contract.ErrConflict
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return result, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_source(tenant_id,purpose,source_id,source_url,source_revision,content_hash,snapshot,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8) ON CONFLICT DO NOTHING`, tenant, purpose, q.Source.ID, q.Source.URL, q.Source.Revision, domain.ContentHash(q.Source.Content), researchJSON(q.Source), q.Source.ObservedAt)
	if e != nil {
		return result, e
	}
	candidates := map[string]bool{}
	matchedKeys := map[string][]string{}
	// Повторное наблюдение того же локального субъекта сохраняет его непрерывность,
	// но не создаёт тождество по домену для разных субъектов страницы.
	rows, e := tx.Query(ctx, `SELECT DISTINCT o.entity_id FROM "lead-management".research_observation o JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE o.tenant_id=$1 AND o.purpose=$2 AND s.source_url=$3 AND o.subject_key=$4 AND s.revoked_at IS NULL`, tenant, purpose, q.Source.URL, q.Subject.Key)
	if e != nil {
		return result, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return result, e
		}
		candidates[id] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return result, e
	}
	for _, id := range q.Identifiers {
		if id.Format != "valid" || id.Authority == "" || id.Jurisdiction == "" {
			continue
		}
		rows, e = tx.Query(ctx, `SELECT DISTINCT i.entity_id FROM "lead-management".research_identifier i JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE i.tenant_id=$1 AND i.purpose=$2 AND i.identifier_key=$3 AND s.revoked_at IS NULL`, tenant, purpose, identifierKey(id))
		if e != nil {
			return result, e
		}
		for rows.Next() {
			var entity string
			if e = rows.Scan(&entity); e != nil {
				rows.Close()
				return result, e
			}
			candidates[entity] = true
			matchedKeys[entity] = append(matchedKeys[entity], identifierKey(id))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return result, e
		}
	}
	entityID := ""
	conflict := len(candidates) > 1
	for id := range candidates {
		entityID = id
	}
	if entityID != "" && !conflict {
		// Несовместимый сильный ID блокирует перенос наблюдения на найденную компанию.
		rows, e = tx.Query(ctx, `SELECT i.snapshot FROM "lead-management".research_identifier i JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE i.tenant_id=$1 AND i.purpose=$2 AND i.entity_id=$3 AND s.revoked_at IS NULL`, tenant, purpose, entityID)
		if e != nil {
			return result, e
		}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return result, e
			}
			var prior contract.Identifier
			if e = json.Unmarshal(raw, &prior); e != nil {
				rows.Close()
				return result, e
			}
			for _, id := range q.Identifiers {
				if id.Format == "valid" && id.Kind == prior.Kind && id.Jurisdiction == prior.Jurisdiction && id.Authority == prior.Authority && id.Normalized != prior.Normalized {
					conflict = true
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return result, e
		}
	}
	created := entityID == "" || conflict
	if created {
		entityID = domain.StableID("ent_", tenant, purpose, q.ObservationID)
	}
	identityKeys := []string{}
	if !created {
		identityKeys = append(identityKeys, matchedKeys[entityID]...)
	}
	now := time.Now().UTC()
	var revision int64
	e = tx.QueryRow(ctx, `INSERT INTO "lead-management".research_entity(tenant_id,purpose,entity_id,kind,country,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,1,$6,$6) ON CONFLICT (tenant_id,purpose,entity_id) DO UPDATE SET revision=research_entity.revision+1,updated_at=EXCLUDED.updated_at RETURNING revision`, tenant, purpose, entityID, q.Subject.Kind, q.Subject.Country, now).Scan(&revision)
	if e != nil {
		return result, e
	}
	result = contract.SaveObservationResult{ObservationID: q.ObservationID, EntityID: entityID, Revision: revision, Created: created, IdentityConflict: conflict}
	_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_observation(tenant_id,purpose,observation_id,payload_hash,entity_id,source_id,subject_key,mission_id,action_id,query_attempt_id,result,recorded_at,identity_keys) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13)`, tenant, purpose, q.ObservationID, hash, entityID, q.Source.ID, q.Subject.Key, q.MissionID, q.ActionID, q.QueryAttemptID, researchJSON(result), now, identityKeys)
	if e != nil {
		return result, e
	}
	claims := append([]contract.Claim{{Predicate: "name", Value: q.Subject.Name, Original: q.Subject.Name, SubjectKey: q.Subject.Key, Locator: q.Subject.Locator, Knowledge: "observed", Method: "source-subject", MethodVersion: "1"}}, q.Claims...)
	for _, id := range q.Identifiers {
		knowledge := "observed"
		if domain.StrongIdentifier(id) && !conflict {
			knowledge = "confirmed"
			_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_identifier(tenant_id,purpose,identifier_key,entity_id,source_id,kind,jurisdiction,authority,normalized,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb) ON CONFLICT DO NOTHING`, tenant, purpose, identifierKey(id), entityID, q.Source.ID, id.Kind, id.Jurisdiction, id.Authority, id.Normalized, researchJSON(id))
			if e != nil {
				return result, e
			}
		}
		claims = append(claims, contract.Claim{Predicate: "identifier:" + id.Jurisdiction + ":" + id.Authority + ":" + id.Kind, Value: id.Normalized, Original: id.Original, SubjectKey: q.Subject.Key, Locator: id.Locator, Knowledge: knowledge, Method: id.Method, MethodVersion: id.MethodVersion})
	}
	for i, c := range claims {
		a := contract.Assertion{ID: domain.StableID("ast_", tenant, purpose, q.ObservationID, fmt.Sprint(i)), IdentityKeys: identityKeys, EntityID: entityID, SourceID: q.Source.ID, ObservationID: q.ObservationID, Predicate: c.Predicate, Value: c.Value, Original: c.Original, SubjectKey: c.SubjectKey, Locator: c.Locator, Knowledge: c.Knowledge, Freshness: "unknown", Access: "available", Method: c.Method, MethodVersion: c.MethodVersion, SourceFamily: q.Source.Family, ObservedAt: q.Source.ObservedAt, RecordedAt: now, ValidFrom: c.ValidFrom, ValidUntil: c.ValidUntil, FreshUntil: c.FreshUntil}
		if conflict && a.Knowledge == "confirmed" {
			a.Knowledge = "conflicted"
		}
		_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_assertion(tenant_id,purpose,assertion_id,entity_id,source_id,observation_id,predicate,value,knowledge,valid_from,valid_until,fresh_until,snapshot,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14)`, tenant, purpose, a.ID, entityID, q.Source.ID, q.ObservationID, a.Predicate, a.Value, a.Knowledge, a.ValidFrom, a.ValidUntil, a.FreshUntil, researchJSON(a), now)
		if e != nil {
			return result, e
		}
	}
	if e = refreshResearchNeeds(ctx, tx, q.Scope, entityID); e != nil {
		return result, e
	}
	if !conflict {
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "lead-management".research_identifier i JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE i.tenant_id=$1 AND i.purpose=$2 AND i.entity_id=$3 AND s.revoked_at IS NULL)`, tenant, purpose, entityID).Scan(&result.IdentityConfirmed)
		if e != nil {
			return result, e
		}
	}
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "lead-management".research_need WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3 AND predicate='contact' AND knowledge_state='satisfied'),EXISTS(SELECT 1 FROM "lead-management".research_need WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3 AND predicate='email' AND knowledge_state='satisfied')`, tenant, purpose, entityID).Scan(&result.HasContact, &result.HasEmail)
	if e != nil {
		return result, e
	}
	_, e = tx.Exec(ctx, `UPDATE "lead-management".research_observation SET result=$4::jsonb WHERE tenant_id=$1 AND purpose=$2 AND observation_id=$3`, tenant, purpose, q.ObservationID, researchJSON(result))
	return result, e
}

// Основание происхождения субъекта — AND со снимком; совпавшие принятые ID
// являются альтернативами OR. Поддержка ссылается только на исходные источники,
// поэтому цикл производных утверждений не может подтвердить сам себя.
const researchIdentitySupport = `NOT EXISTS(SELECT 1 FROM "lead-management".research_observation o WHERE o.tenant_id=a.tenant_id AND o.purpose=a.purpose AND o.observation_id=a.observation_id AND cardinality(o.identity_keys)>0 AND NOT EXISTS(SELECT 1 FROM "lead-management".research_identifier i JOIN "lead-management".research_source grounding USING(tenant_id,purpose,source_id) WHERE i.tenant_id=o.tenant_id AND i.purpose=o.purpose AND i.entity_id=o.entity_id AND i.identifier_key=ANY(o.identity_keys) AND grounding.revoked_at IS NULL))`
const researchSameIdentityGroup = `(a.entity_id=$3 OR a.entity_id IN (SELECT member.entity_id FROM "lead-management".research_identity_member member WHERE member.tenant_id=$1 AND member.purpose=$2 AND member.canonical_entity_id=(SELECT own.canonical_entity_id FROM "lead-management".research_identity_member own WHERE own.tenant_id=$1 AND own.purpose=$2 AND own.entity_id=$3)))`
const currentResearchContact = researchIdentitySupport + ` AND a.knowledge='confirmed' AND s.revoked_at IS NULL AND (a.valid_from IS NULL OR a.valid_from<=now()) AND (a.valid_until IS NULL OR a.valid_until>now()) AND a.fresh_until>now()`

func refreshResearchNeeds(ctx context.Context, tx pgx.Tx, s contract.Scope, entity string) error {
	for _, predicate := range []string{"contact", "email"} {
		var present bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=$1 AND a.purpose=$2 AND `+researchSameIdentityGroup+` AND (a.predicate=$4 OR ($4='contact' AND a.predicate IN ('email','phone'))) AND `+currentResearchContact+`)`, s.TenantID, s.Purpose, entity, predicate).Scan(&present)
		if e != nil {
			return e
		}
		state, reason := "open", "Нет актуальной подтверждённой связи контакта с субъектом"
		if present {
			state = "satisfied"
			reason = "Актуальное предметное свидетельство подтверждает связь; достижимость не проверена"
		}
		_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_need(tenant_id,purpose,need_id,entity_id,predicate,criteria_version,knowledge_state,reason,updated_at) VALUES($1,$2,$3,$4,$5,'subject-contact-v1',$6,$7,now()) ON CONFLICT(tenant_id,purpose,entity_id,predicate,criteria_version) DO UPDATE SET knowledge_state=EXCLUDED.knowledge_state,reason=EXCLUDED.reason,updated_at=EXCLUDED.updated_at`, s.TenantID, s.Purpose, domain.StableID("need_", s.TenantID, s.Purpose, entity, predicate), entity, predicate, state, reason)
		if e != nil {
			return e
		}
	}
	return nil
}

func readResearchEntity(ctx context.Context, tx pgx.Tx, s contract.Scope, id string) (contract.Entity, error) {
	var e contract.Entity
	err := tx.QueryRow(ctx, `SELECT e.entity_id,COALESCE((SELECT a.value FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=e.tenant_id AND a.purpose=e.purpose AND a.entity_id=e.entity_id AND a.predicate='name' AND s.revoked_at IS NULL ORDER BY a.recorded_at DESC,a.assertion_id LIMIT 1),''),e.kind,e.country,e.revision,e.created_at,e.updated_at FROM "lead-management".research_entity e WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3`, s.TenantID, s.Purpose, id).Scan(&e.ID, &e.Name, &e.Kind, &e.Country, &e.Revision, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, contract.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	if e.Name == "" {
		return e, contract.ErrRevoked
	}
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=$1 AND a.purpose=$2 AND `+researchSameIdentityGroup+` AND a.predicate IN ('email','phone') AND `+currentResearchContact+`),EXISTS(SELECT 1 FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=$1 AND a.purpose=$2 AND `+researchSameIdentityGroup+` AND a.predicate='email' AND `+currentResearchContact+`)`, s.TenantID, s.Purpose, id).Scan(&e.HasContact, &e.HasEmail)
	return e, err
}
func (r *ResearchMemoryRepository) Get(ctx context.Context, q contract.GetRequest) (contract.Dossier, error) {
	var d contract.Dossier
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return d, e
	}
	defer tx.Rollback(ctx)
	if e = ensureResearchIdentity(ctx, tx, q.Scope); e != nil {
		return d, e
	}
	d.IdentityGroup, e = readResearchIdentityGroup(ctx, tx, q.Scope, q.EntityID)
	if e != nil {
		return d, e
	}
	d.Entity, e = readResearchEntity(ctx, tx, q.Scope, q.EntityID)
	if e != nil {
		return d, e
	}
	for _, id := range d.IdentityGroup.MemberEntityIDs {
		if e = refreshResearchNeeds(ctx, tx, q.Scope, id); e != nil {
			return d, e
		}
	}
	rows, e := tx.Query(ctx, `SELECT a.snapshot,s.revoked_at IS NOT NULL,`+researchIdentitySupport+` FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=$1 AND a.purpose=$2 AND a.entity_id=ANY($3) ORDER BY a.recorded_at,a.assertion_id`, q.Scope.TenantID, q.Scope.Purpose, d.IdentityGroup.MemberEntityIDs)
	if e != nil {
		return d, e
	}
	d.Assertions = []contract.Assertion{}
	sourceIDs := map[string]bool{}
	now := time.Now()
	for rows.Next() {
		var raw []byte
		var revoked, supported bool
		if e = rows.Scan(&raw, &revoked, &supported); e != nil {
			rows.Close()
			return d, e
		}
		var a contract.Assertion
		if e = json.Unmarshal(raw, &a); e != nil {
			rows.Close()
			return d, e
		}
		a.SupportState = "satisfied"
		if !supported || revoked {
			a.SupportState = "insufficient"
		}
		if revoked {
			a.Value = ""
			a.Original = ""
			a.Locator = contract.Locator{}
			a.SubjectKey = ""
			a.SourceFamily = ""
			a.Access = "revoked"
		} else {
			sourceIDs[a.SourceID] = true
		}
		if a.FreshUntil == nil {
			a.Freshness = "unknown"
		} else if a.FreshUntil.After(now) {
			a.Freshness = "current"
		} else {
			a.Freshness = "stale"
		}
		d.Assertions = append(d.Assertions, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return d, e
	}
	for i := range d.Assertions {
		a := &d.Assertions[i]
		if a.Access == "revoked" {
			continue
		}
		a.SupportSources = []string{a.SourceID}
		if len(a.IdentityKeys) == 0 {
			continue
		}
		grounding, e := tx.Query(ctx, `SELECT DISTINCT i.source_id FROM "lead-management".research_identifier i JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE i.tenant_id=$1 AND i.purpose=$2 AND i.entity_id=$3 AND i.identifier_key=ANY($4) AND s.revoked_at IS NULL ORDER BY i.source_id`, q.Scope.TenantID, q.Scope.Purpose, a.EntityID, a.IdentityKeys)
		if e != nil {
			return d, e
		}
		for grounding.Next() {
			var id string
			if e = grounding.Scan(&id); e != nil {
				grounding.Close()
				return d, e
			}
			if id != a.SourceID {
				a.SupportSources = append(a.SupportSources, id)
			}
			sourceIDs[id] = true
		}
		e = grounding.Err()
		grounding.Close()
		if e != nil {
			return d, e
		}
	}
	d.Sources = []contract.Source{}
	keys := make([]string, 0, len(sourceIDs))
	for id := range sourceIDs {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		var raw []byte
		if e = tx.QueryRow(ctx, `SELECT snapshot FROM "lead-management".research_source WHERE tenant_id=$1 AND purpose=$2 AND source_id=$3 AND revoked_at IS NULL`, q.Scope.TenantID, q.Scope.Purpose, id).Scan(&raw); e != nil {
			return d, e
		}
		var src contract.Source
		if e = json.Unmarshal(raw, &src); e != nil {
			return d, e
		}
		d.Sources = append(d.Sources, src)
	}
	rows, e = tx.Query(ctx, `SELECT n.need_id,n.entity_id,n.predicate,n.criteria_version,n.knowledge_state,n.reason,n.updated_at,COALESCE(array_agg(r.mission_id ORDER BY r.mission_id) FILTER(WHERE r.mission_id IS NOT NULL),ARRAY[]::text[]) FROM "lead-management".research_need n LEFT JOIN "lead-management".research_need_run r USING(tenant_id,purpose,need_id) WHERE n.tenant_id=$1 AND n.purpose=$2 AND n.entity_id=ANY($3) GROUP BY n.tenant_id,n.purpose,n.need_id ORDER BY n.predicate,n.entity_id`, q.Scope.TenantID, q.Scope.Purpose, d.IdentityGroup.MemberEntityIDs)
	if e != nil {
		return d, e
	}
	d.Needs = []contract.OpenNeed{}
	for rows.Next() {
		n := contract.OpenNeed{EntityID: q.EntityID, ExecutionState: "dormant"}
		if e = rows.Scan(&n.ID, &n.EntityID, &n.Predicate, &n.CriteriaVersion, &n.KnowledgeState, &n.Reason, &n.UpdatedAt, &n.MissionIDs); e != nil {
			rows.Close()
			return d, e
		}
		if len(n.MissionIDs) > 0 {
			n.ExecutionState = "attached"
		}
		d.Needs = append(d.Needs, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return d, e
	}
	if e = ensureResearchIdentity(ctx, tx, q.Scope); e != nil {
		return d, e
	}
	d.IdentityGroup, e = readResearchIdentityGroup(ctx, tx, q.Scope, q.EntityID)
	if e != nil {
		return d, e
	}
	d.Entity.CanonicalEntityID = d.IdentityGroup.CanonicalEntityID
	d.Entity.IdentityRevision = d.IdentityGroup.ProjectionRevision
	d.IdentityCandidates, e = readResearchIdentityCandidates(ctx, tx, q.Scope, d.IdentityGroup.MemberEntityIDs)
	if e != nil {
		return d, e
	}
	d.Entity.CandidateIDs = pendingResearchCandidateIDs(d.IdentityCandidates)
	d.Entity.IdentityReviewRequired = len(d.Entity.CandidateIDs) > 0
	d.IdentityDecisions, e = readResearchIdentityHistory(ctx, tx, q.Scope, d.IdentityGroup.MemberEntityIDs, 100)
	if e != nil {
		return d, e
	}
	return d, tx.Commit(ctx)
}
func (r *ResearchMemoryRepository) List(ctx context.Context, q contract.ListRequest) (contract.ListResult, error) {
	result := contract.ListResult{Entities: []contract.Entity{}}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 || q.Offset < 0 {
		return result, contract.ErrInvalid
	}
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return result, e
	}
	defer tx.Rollback(ctx)
	if e = ensureResearchIdentity(ctx, tx, q.Scope); e != nil {
		return result, e
	}
	const filter = ` FROM "lead-management".research_entity e JOIN "lead-management".research_identity_member identity USING(tenant_id,purpose,entity_id) WHERE e.tenant_id=$1 AND e.purpose=$2 AND ($3='' OR EXISTS(SELECT 1 FROM "lead-management".research_observation o WHERE o.tenant_id=e.tenant_id AND o.purpose=e.purpose AND o.entity_id=e.entity_id AND o.mission_id=$3)) AND EXISTS(SELECT 1 FROM "lead-management".research_assertion a JOIN "lead-management".research_source s USING(tenant_id,purpose,source_id) WHERE a.tenant_id=e.tenant_id AND a.purpose=e.purpose AND a.entity_id=e.entity_id AND s.revoked_at IS NULL AND ($4='' OR a.search_text @@ plainto_tsquery('simple',$4) OR lower(a.value)=lower($4)))`
	if e = tx.QueryRow(ctx, `SELECT count(DISTINCT identity.canonical_entity_id)`+filter, q.Scope.TenantID, q.Scope.Purpose, q.MissionID, q.Query).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := tx.Query(ctx, `SELECT identity.canonical_entity_id`+filter+` GROUP BY identity.canonical_entity_id ORDER BY max(e.updated_at) DESC,identity.canonical_entity_id LIMIT $5 OFFSET $6`, q.Scope.TenantID, q.Scope.Purpose, q.MissionID, q.Query, q.Limit, q.Offset)
	if e != nil {
		return result, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return result, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return result, e
	}
	for _, id := range ids {
		entity, e := readResearchEntity(ctx, tx, q.Scope, id)
		if e != nil {
			return result, e
		}
		group, e := readResearchIdentityGroup(ctx, tx, q.Scope, id)
		if e != nil {
			return result, e
		}
		entity.CanonicalEntityID = group.CanonicalEntityID
		entity.IdentityRevision = group.ProjectionRevision
		candidates, e := readResearchIdentityCandidates(ctx, tx, q.Scope, group.MemberEntityIDs)
		if e != nil {
			return result, e
		}
		entity.CandidateIDs = pendingResearchCandidateIDs(candidates)
		entity.IdentityReviewRequired = len(entity.CandidateIDs) > 0
		result.Entities = append(result.Entities, entity)
	}
	return result, tx.Commit(ctx)
}

func (r *ResearchMemoryRepository) SaveAttempt(ctx context.Context, a contract.Attempt) error {
	tx, e := r.begin(ctx, a.Scope)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if a.EntityID != "" {
		if _, e = readResearchEntity(ctx, tx, a.Scope, a.EntityID); e != nil {
			return e
		}
	}
	hash := domain.Hash(a)
	var old string
	e = tx.QueryRow(ctx, `SELECT payload_hash FROM "lead-management".research_attempt WHERE tenant_id=$1 AND purpose=$2 AND attempt_id=$3`, a.Scope.TenantID, a.Scope.Purpose, a.ID).Scan(&old)
	if e == nil {
		if old != hash {
			return contract.ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_attempt(tenant_id,purpose,attempt_id,payload_hash,entity_id,question,source_url,source_revision,locale,snapshot,expires_at,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12)`, a.Scope.TenantID, a.Scope.Purpose, a.ID, hash, a.EntityID, a.Question, a.SourceURL, a.SourceRevision, a.Locale, researchJSON(a), a.ExpiresAt, a.ObservedAt)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *ResearchMemoryRepository) RecallAttempts(ctx context.Context, q contract.RecallAttemptsRequest) ([]contract.Attempt, error) {
	if e := domain.ValidateScope(q.Scope); e != nil {
		return nil, e
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 100 {
		return nil, contract.ErrInvalid
	}
	// Anti-join отзыва работает до удаления индексов; ключ памяти не содержит mission.
	rows, e := r.pool.Query(ctx, `SELECT a.snapshot FROM "lead-management".research_attempt a WHERE tenant_id=$1 AND purpose=$2 AND question=$3 AND source_url=$4 AND source_revision=$5 AND locale=$6 AND entity_id=$7 AND expires_at>now() AND NOT EXISTS(SELECT 1 FROM "lead-management".research_source s WHERE s.tenant_id=a.tenant_id AND s.purpose=a.purpose AND s.source_url=a.source_url AND s.source_revision=a.source_revision AND s.revoked_at IS NOT NULL) ORDER BY observed_at DESC,attempt_id LIMIT $8`, q.Scope.TenantID, q.Scope.Purpose, q.Question, q.SourceURL, q.SourceRevision, q.Locale, q.EntityID, q.Limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contract.Attempt{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var a contract.Attempt
		if e = json.Unmarshal(raw, &a); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *ResearchMemoryRepository) SetNeedExecution(ctx context.Context, q contract.NeedExecutionRequest) error {
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = readResearchEntity(ctx, tx, q.Scope, q.EntityID); e != nil {
		return e
	}
	if q.Attach {
		// Память не выдаёт разрешение запуска: только связывает с сохранённым результатом
		// уже принятой миссии; HTTP/командный адаптер обязан проверить её полномочия.
		var observed bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "lead-management".research_observation WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3 AND mission_id=$4)`, q.Scope.TenantID, q.Scope.Purpose, q.EntityID, q.MissionID).Scan(&observed)
		if e != nil {
			return e
		}
		if !observed {
			return contract.ErrNotFound
		}
		_, e = tx.Exec(ctx, `INSERT INTO "lead-management".research_need_run(tenant_id,purpose,need_id,mission_id) SELECT tenant_id,purpose,need_id,$4 FROM "lead-management".research_need WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3 ON CONFLICT DO NOTHING`, q.Scope.TenantID, q.Scope.Purpose, q.EntityID, q.MissionID)
	} else {
		_, e = tx.Exec(ctx, `DELETE FROM "lead-management".research_need_run r USING "lead-management".research_need n WHERE r.tenant_id=n.tenant_id AND r.purpose=n.purpose AND r.need_id=n.need_id AND n.tenant_id=$1 AND n.purpose=$2 AND n.entity_id=$3 AND r.mission_id=$4`, q.Scope.TenantID, q.Scope.Purpose, q.EntityID, q.MissionID)
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *ResearchMemoryRepository) RevokeSource(ctx context.Context, q contract.RevokeSourceRequest) error {
	tx, e := r.begin(ctx, q.Scope)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE "lead-management".research_source SET revoked_at=COALESCE(revoked_at,now()),revoke_reason=CASE WHEN revoked_at IS NULL THEN $4 ELSE revoke_reason END WHERE tenant_id=$1 AND purpose=$2 AND source_id=$3 AND revoked_at IS NULL`, q.Scope.TenantID, q.Scope.Purpose, q.SourceID, q.Reason)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "lead-management".research_source WHERE tenant_id=$1 AND purpose=$2 AND source_id=$3)`, q.Scope.TenantID, q.Scope.Purpose, q.SourceID).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return contract.ErrNotFound
		}
		return tx.Commit(ctx)
	}
	rows, e := tx.Query(ctx, `SELECT DISTINCT entity_id FROM "lead-management".research_observation WHERE tenant_id=$1 AND purpose=$2 AND source_id=$3`, q.Scope.TenantID, q.Scope.Purpose, q.SourceID)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = refreshResearchNeeds(ctx, tx, q.Scope, id); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE "lead-management".research_entity SET revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND purpose=$2 AND entity_id=$3`, q.Scope.TenantID, q.Scope.Purpose, id)
		if e != nil {
			return e
		}
	}
	if e = rebuildResearchIdentity(ctx, tx, q.Scope, "source-revoke:"+q.SourceID, false); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
