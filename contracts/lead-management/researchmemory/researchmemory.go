// Package researchmemory публикует исследовательскую память lead-management.
// Контракт содержит красные данные; вызывающий адаптер обязан проверить полномочия
// до передачи явно заданного tenant и назначения обработки.
package researchmemory

import (
	"context"
	"errors"
	"time"
)

const ContractName = "research-memory"
const SchemaVersion = "1.0.0"

var (
	ErrInvalid  = errors.New("researchmemory: некорректный запрос")
	ErrNotFound = errors.New("researchmemory: запись не найдена в разрешённой области")
	ErrConflict = errors.New("researchmemory: конфликт версии или ключа идемпотентности")
	ErrRevoked  = errors.New("researchmemory: использование источника отозвано")
)

type Scope struct {
	TenantID string `json:"tenant_id"`
	Purpose  string `json:"purpose"`
}
type Locator struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Quote string `json:"quote"`
}
type Source struct {
	ID             string    `json:"id,omitempty"`
	URL            string    `json:"url"`
	Revision       string    `json:"revision"`
	Representation string    `json:"representation"`
	Content        string    `json:"content"`
	ContentHash    string    `json:"content_hash"`
	ObservedAt     time.Time `json:"observed_at"`
	Family         string    `json:"family"`
	// Kind: page, registry, search-snippet. Registry требует явного Authority.
	Kind      string `json:"kind"`
	Authority string `json:"authority,omitempty"`
}
type Subject struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Country string  `json:"country,omitempty"`
	Locator Locator `json:"locator"`
}
type Identifier struct {
	Kind         string `json:"kind"`
	Jurisdiction string `json:"jurisdiction"`
	Authority    string `json:"authority"`
	Original     string `json:"original"`
	Normalized   string `json:"normalized,omitempty"`
	Format       string `json:"format,omitempty"`
	// Existence остаётся unknown, кроме registry-record надлежащего Authority.
	Existence     string  `json:"existence,omitempty"`
	Method        string  `json:"method"`
	MethodVersion string  `json:"method_version"`
	Locator       Locator `json:"locator"`
}
type Claim struct {
	Predicate     string     `json:"predicate"`
	Value         string     `json:"value"`
	Original      string     `json:"original,omitempty"`
	SubjectKey    string     `json:"subject_key"`
	Locator       Locator    `json:"locator"`
	Knowledge     string     `json:"knowledge,omitempty"`
	Method        string     `json:"method"`
	MethodVersion string     `json:"method_version"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	FreshUntil    *time.Time `json:"fresh_until,omitempty"`
}
type SaveObservationRequest struct {
	Scope          Scope        `json:"scope"`
	ObservationID  string       `json:"observation_id"`
	MissionID      string       `json:"mission_id"`
	ActionID       string       `json:"action_id"`
	QueryAttemptID string       `json:"query_attempt_id"`
	Source         Source       `json:"source"`
	Subject        Subject      `json:"subject"`
	Identifiers    []Identifier `json:"identifiers,omitempty"`
	Claims         []Claim      `json:"claims,omitempty"`
}
type SaveObservationResult struct {
	IdentityReviewRequired bool     `json:"identity_review_required"`
	CandidateIDs           []string `json:"candidate_ids,omitempty"`
	CanonicalEntityID      string   `json:"canonical_entity_id"`
	IdentityRevision       int64    `json:"identity_revision"`
	ObservationID          string   `json:"observation_id"`
	EntityID               string   `json:"entity_id"`
	Revision               int64    `json:"revision"`
	Created                bool     `json:"created"`
	Duplicate              bool     `json:"duplicate"`
	HasContact             bool     `json:"has_contact"`
	HasEmail               bool     `json:"has_email"`
	IdentityConflict       bool     `json:"identity_conflict"`
	IdentityConfirmed      bool     `json:"identity_confirmed"`
}
type Entity struct {
	IdentityReviewRequired bool      `json:"identity_review_required"`
	CandidateIDs           []string  `json:"candidate_ids,omitempty"`
	CanonicalEntityID      string    `json:"canonical_entity_id"`
	IdentityRevision       int64     `json:"identity_revision"`
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Kind                   string    `json:"kind"`
	Country                string    `json:"country,omitempty"`
	Revision               int64     `json:"revision"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	HasContact             bool      `json:"has_contact"`
	HasEmail               bool      `json:"has_email"`
}
type Assertion struct {
	SupportState   string     `json:"support_state"`
	IdentityKeys   []string   `json:"identity_keys,omitempty"`
	SupportSources []string   `json:"support_sources,omitempty"`
	ID             string     `json:"id"`
	EntityID       string     `json:"entity_id"`
	SourceID       string     `json:"source_id"`
	ObservationID  string     `json:"observation_id"`
	Predicate      string     `json:"predicate"`
	Value          string     `json:"value"`
	Original       string     `json:"original"`
	SubjectKey     string     `json:"subject_key"`
	Locator        Locator    `json:"locator"`
	Knowledge      string     `json:"knowledge"`
	Freshness      string     `json:"freshness"`
	Access         string     `json:"access"`
	Method         string     `json:"method"`
	MethodVersion  string     `json:"method_version"`
	SourceFamily   string     `json:"source_family"`
	ObservedAt     time.Time  `json:"observed_at"`
	RecordedAt     time.Time  `json:"recorded_at"`
	ValidFrom      *time.Time `json:"valid_from,omitempty"`
	ValidUntil     *time.Time `json:"valid_until,omitempty"`
	FreshUntil     *time.Time `json:"fresh_until,omitempty"`
}
type OpenNeed struct {
	ID              string    `json:"id"`
	EntityID        string    `json:"entity_id"`
	Predicate       string    `json:"predicate"`
	CriteriaVersion string    `json:"criteria_version"`
	KnowledgeState  string    `json:"knowledge_state"`
	ExecutionState  string    `json:"execution_state"`
	MissionIDs      []string  `json:"mission_ids"`
	Reason          string    `json:"reason"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type Dossier struct {
	IdentityCandidates []IdentityCandidate `json:"identity_candidates"`
	IdentityGroup      IdentityGroup       `json:"identity_group"`
	IdentityDecisions  []IdentityDecision  `json:"identity_decisions"`
	Entity             Entity              `json:"entity"`
	Assertions         []Assertion         `json:"assertions"`
	Sources            []Source            `json:"sources"`
	Needs              []OpenNeed          `json:"needs"`
}
type GetRequest struct {
	Scope    Scope  `json:"scope"`
	EntityID string `json:"entity_id"`
}
type ListRequest struct {
	Scope     Scope  `json:"scope"`
	MissionID string `json:"mission_id,omitempty"`
	Query     string `json:"query,omitempty"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}
type ListResult struct {
	Entities []Entity `json:"entities"`
	Total    int      `json:"total"`
}
type Attempt struct {
	Scope          Scope     `json:"scope"`
	ID             string    `json:"id"`
	MissionID      string    `json:"mission_id"`
	ActionID       string    `json:"action_id"`
	EntityID       string    `json:"entity_id,omitempty"`
	Question       string    `json:"question"`
	SourceURL      string    `json:"source_url"`
	SourceRevision string    `json:"source_revision"`
	Locale         string    `json:"locale"`
	Tool           string    `json:"tool"`
	Outcome        string    `json:"outcome"`
	Reason         string    `json:"reason"`
	ObservedAt     time.Time `json:"observed_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}
type RecallAttemptsRequest struct {
	Scope          Scope  `json:"scope"`
	Question       string `json:"question"`
	SourceURL      string `json:"source_url"`
	SourceRevision string `json:"source_revision"`
	Locale         string `json:"locale"`
	EntityID       string `json:"entity_id,omitempty"`
	Limit          int    `json:"limit"`
}
type NeedExecutionRequest struct {
	Scope     Scope  `json:"scope"`
	EntityID  string `json:"entity_id"`
	MissionID string `json:"mission_id"`
	Attach    bool   `json:"attach"`
}
type RevokeSourceRequest struct {
	Scope    Scope  `json:"scope"`
	SourceID string `json:"source_id"`
	Reason   string `json:"reason"`
}

type Port interface {
	SaveObservation(context.Context, SaveObservationRequest) (SaveObservationResult, error)
	Get(context.Context, GetRequest) (Dossier, error)
	List(context.Context, ListRequest) (ListResult, error)
	SaveAttempt(context.Context, Attempt) error
	RecallAttempts(context.Context, RecallAttemptsRequest) ([]Attempt, error)
	SetNeedExecution(context.Context, NeedExecutionRequest) error
	RevokeSource(context.Context, RevokeSourceRequest) error
}
