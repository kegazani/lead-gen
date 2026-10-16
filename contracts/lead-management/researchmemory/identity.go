package researchmemory

import (
	"context"
	"time"
)

// IdentityPort — отдельные команды тождества. Адаптер проверяет человеческого
// автора и права; LLM может только показать кандидатов, не выполнить команду.
type IdentityPort interface {
	DecideIdentity(context.Context, IdentityDecisionRequest) (IdentityDecisionResult, error)
	ResolveIdentity(context.Context, GetRequest) (IdentityGroup, error)
	ListIdentityDecisions(context.Context, IdentityHistoryRequest) ([]IdentityDecision, error)
	ResolveIdentityBatch(context.Context, IdentityBatchRequest) ([]IdentityEntityState, error)
	ReadIdentityChanges(context.Context, IdentityChangesRequest) ([]IdentityChange, error)
}
type IdentityDecisionRequest struct {
	Scope                Scope            `json:"scope"`
	DecisionID           string           `json:"decision_id"`
	Action               string           `json:"action"`
	LeftEntityID         string           `json:"left_entity_id,omitempty"`
	RightEntityID        string           `json:"right_entity_id,omitempty"`
	ExpectedRevisions    map[string]int64 `json:"expected_revisions"`
	Actor                string           `json:"actor"`
	Reason               string           `json:"reason"`
	EvidenceAssertionIDs []string         `json:"evidence_assertion_ids,omitempty"`
	RevokeDecisionID     string           `json:"revoke_decision_id,omitempty"`
}
type IdentityDecision struct {
	ID                   string           `json:"id"`
	Action               string           `json:"action"`
	LeftEntityID         string           `json:"left_entity_id"`
	RightEntityID        string           `json:"right_entity_id"`
	ExpectedRevisions    map[string]int64 `json:"expected_revisions"`
	Actor                string           `json:"actor"`
	Reason               string           `json:"reason"`
	EvidenceAssertionIDs []string         `json:"evidence_assertion_ids"`
	RevokeDecisionID     string           `json:"revoke_decision_id,omitempty"`
	Method               string           `json:"method"`
	MethodVersion        string           `json:"method_version"`
	RecordedAt           time.Time        `json:"recorded_at"`
	// State — текущая проекция: active, revoked, unavailable, conflict.
	State              string `json:"state"`
	ProjectionRevision int64  `json:"projection_revision"`
}
type IdentityGroup struct {
	MemberRevisions    map[string]int64 `json:"member_revisions"`
	CanonicalEntityID  string           `json:"canonical_entity_id"`
	MemberEntityIDs    []string         `json:"member_entity_ids"`
	ProjectionRevision int64            `json:"projection_revision"`
	DecisionIDs        []string         `json:"decision_ids"`
}
type IdentityDecisionResult struct {
	Decision  IdentityDecision `json:"decision"`
	Groups    []IdentityGroup  `json:"groups"`
	Duplicate bool             `json:"duplicate"`
}
type IdentityHistoryRequest struct {
	Scope    Scope  `json:"scope"`
	EntityID string `json:"entity_id,omitempty"`
	Limit    int    `json:"limit"`
}

// IdentityBatchRequest разрешает известные исходные refs до вычисления текущего
// зачёта активной миссии; терминальные снимки потребитель не переписывает.
type IdentityBatchRequest struct {
	Scope     Scope    `json:"scope"`
	EntityIDs []string `json:"entity_ids"`
}
type IdentityEntityState struct {
	Name                       string           `json:"name"`
	GroundedIdentifiers        []IdentityGround `json:"grounded_identifiers"`
	DistinctCanonicalEntityIDs []string         `json:"distinct_canonical_entity_ids"`
	IdentityReviewRequired     bool             `json:"identity_review_required"`
	CandidateIDs               []string         `json:"candidate_ids"`
	EntityID                   string           `json:"entity_id"`
	EntityRevision             int64            `json:"entity_revision"`
	CanonicalEntityID          string           `json:"canonical_entity_id"`
	IdentityRevision           int64            `json:"identity_revision"`
	HasContact                 bool             `json:"has_contact"`
	HasEmail                   bool             `json:"has_email"`
	Available                  bool             `json:"available"`
}
type IdentityChangesRequest struct {
	Scope         Scope `json:"scope"`
	AfterRevision int64 `json:"after_revision"`
	Limit         int   `json:"limit"`
}
type IdentityChange struct {
	Revision  int64     `json:"revision"`
	EventID   string    `json:"event_id"`
	Cause     string    `json:"cause"`
	EntityIDs []string  `json:"entity_ids"`
	CreatedAt time.Time `json:"created_at"`
}

// IdentityGround — действующее реестровое основание; строки разных namespace
// нельзя сравнивать как один регистрационный номер.
type IdentityGround struct {
	Kind         string `json:"kind"`
	Jurisdiction string `json:"jurisdiction"`
	Authority    string `json:"authority"`
	Value        string `json:"value"`
}
type IdentityCandidate struct {
	ID                   string    `json:"id"`
	LeftEntityID         string    `json:"left_entity_id"`
	RightEntityID        string    `json:"right_entity_id"`
	State                string    `json:"state"`
	Signals              []string  `json:"signals"`
	SourceID             string    `json:"source_id"`
	TargetSourceID       string    `json:"target_source_id"`
	Navigation           Locator   `json:"navigation"`
	EvidenceAssertionIDs []string  `json:"evidence_assertion_ids"`
	CreatedAt            time.Time `json:"created_at"`
}
