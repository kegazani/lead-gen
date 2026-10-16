// Package researchmission публикует поручение и согласованный снимок исследования.
// Человеческое разрешение проверяется владельцем заявки до вызова Start.
package researchmission

import (
	"context"
	"errors"
	"strings"
	"time"

	"lead-gen/contracts/agent-orchestration/researchplan"
)

const ContractName = "research-mission"
const SchemaVersion = "1.0.0"

var (
	ErrInvalid     = errors.New("researchmission: некорректное поручение")
	ErrNotFound    = errors.New("researchmission: миссия не найдена в области доступа")
	ErrConflict    = errors.New("researchmission: конфликт версии или повторного поручения")
	ErrUnavailable = errors.New("researchmission: исполнитель недоступен")
)

type Scope struct {
	TenantID  string `json:"tenant_id"`
	PurposeID string `json:"purpose_id"`
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.PurposeID) == "" {
		return ErrInvalid
	}
	return nil
}

// TechnicalLimits не содержит цены или денежного резерва для proxy-инструментов.
type TechnicalLimits struct {
	MaxActions           int       `json:"max_actions"`
	MaxAttempts          int       `json:"max_attempts"`
	MaxResultsPerSearch  int       `json:"max_results_per_search"`
	MaxDepth             int       `json:"max_depth"`
	MaxBytesPerArtifact  int64     `json:"max_bytes_per_artifact"`
	ActionTimeoutSeconds int       `json:"action_timeout_seconds"`
	Deadline             time.Time `json:"deadline"`
}

func (l TechnicalLimits) Validate() error {
	if l.MaxActions < 1 || l.MaxActions > 10000 || l.MaxAttempts < 1 || l.MaxAttempts > 10 || l.MaxResultsPerSearch < 1 || l.MaxResultsPerSearch > 100 || l.MaxDepth < 0 || l.MaxDepth > 10 || l.MaxBytesPerArtifact < 1 || l.MaxBytesPerArtifact > 16<<20 || l.ActionTimeoutSeconds < 1 || l.ActionTimeoutSeconds > 300 || l.Deadline.IsZero() {
		return ErrInvalid
	}
	return nil
}

type StartRequest struct {
	Scope               Scope             `json:"scope"`
	IntentID            string            `json:"intent_id"`
	Actor               string            `json:"actor"`
	Generation          string            `json:"generation"`
	CriteriaVersion     int               `json:"criteria_version"`
	Plan                researchplan.Plan `json:"plan"`
	Limits              TechnicalLimits   `json:"limits"`
	FirecrawlGrantRef   string            `json:"firecrawl_grant_ref"`
	FirecrawlMaxCredits int               `json:"firecrawl_max_credits"`
}
type ControlRequest struct {
	Scope            Scope  `json:"scope"`
	MissionID        string `json:"mission_id"`
	Actor            string `json:"actor"`
	Command          string `json:"command"`
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type Counters struct {
	Discovered   int `json:"discovered"`
	Documents    int `json:"documents"`
	Saved        int `json:"saved"`
	Accepted     int `json:"accepted"`
	ContactReady int `json:"contact_ready"`
	EmailReady   int `json:"email_ready"`
	LateUpdates  int `json:"late_updates"`
}
type CriteriaAssessment struct {
	ObservationID   string   `json:"observation_id"`
	CriteriaVersion int      `json:"criteria_version"`
	Accepted        bool     `json:"accepted"`
	EmailReady      bool     `json:"email_ready"`
	Reason          string   `json:"reason"`
	EvidenceRefs    []string `json:"evidence_refs"`
}
type Member struct {
	AssessmentSets             []CriteriaAssessment `json:"assessment_sets,omitempty"`
	Name                       string               `json:"name"`
	DistinctCanonicalEntityIDs []string             `json:"distinct_canonical_entity_ids,omitempty"`
	CanonicalEntityID          string               `json:"canonical_entity_id"`
	IdentityRevision           int64                `json:"identity_revision"`
	OriginalEntityIDs          []string             `json:"original_entity_ids,omitempty"`
	CriteriaAccepted           bool                 `json:"criteria_accepted"`
	SourceUnavailable          bool                 `json:"source_unavailable"`
	CurrentEntityRevision      int64                `json:"current_entity_revision"`
	EntityID                   string               `json:"entity_id"`
	EntityRevision             int64                `json:"entity_revision"`
	ReceiptID                  string               `json:"receipt_id"`
	ActionID                   string               `json:"action_id"`
	CriteriaVersion            int                  `json:"criteria_version"`
	State                      string               `json:"state"`
	Accepted                   bool                 `json:"accepted"`
	HasContact                 bool                 `json:"has_contact"`
	HasEmail                   bool                 `json:"has_email"`
	EmailReady                 bool                 `json:"email_ready"`
	Reason                     string               `json:"reason"`
	EvidenceRefs               []string             `json:"evidence_refs,omitempty"`
	UpdatedAt                  time.Time            `json:"updated_at"`
}
type ActionStatus struct {
	SettlementState  string     `json:"settlement_state,omitempty"`
	SettlementReason string     `json:"settlement_reason,omitempty"`
	SettlementNextAt *time.Time `json:"settlement_next_at,omitempty"`
	Diagnostics      []string   `json:"diagnostics,omitempty"`
	ID               string     `json:"id"`
	Kind             string     `json:"kind"`
	Queue            string     `json:"queue"`
	SourceID         string     `json:"source_id"`
	StrategyCellID   string     `json:"strategy_cell_id"`
	Country          string     `json:"country"`
	Language         string     `json:"language"`
	State            string     `json:"state"`
	Attempt          int        `json:"attempt"`
	Reason           string     `json:"reason"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
}

// FirecrawlBudget — проекция владельца учёта, не второй ledger.
type FirecrawlBudget struct {
	Available bool   `json:"available"`
	GrantRef  string `json:"grant_ref"`
	Actual    int    `json:"actual"`
	Reserved  int    `json:"reserved"`
	Unknown   int    `json:"unknown"`
	Exhausted bool   `json:"exhausted"`
}

// Warning связывает предупреждение с сохранённым действием/субъектом, не
// раскрывая сырой prompt или непроверенный текст ответа модели.
type Warning struct {
	Code     string `json:"code"`
	ActionID string `json:"action_id"`
	SourceID string `json:"source_id"`
	Country  string `json:"country"`
	Language string `json:"language"`
	EntityID string `json:"entity_id,omitempty"`
}
type Snapshot struct {
	Warnings        []Warning                      `json:"warnings,omitempty"`
	Scope           Scope                          `json:"scope"`
	MissionID       string                         `json:"mission_id"`
	IntentID        string                         `json:"intent_id"`
	Revision        int64                          `json:"revision"`
	CriteriaVersion int                            `json:"criteria_version"`
	Goal            researchplan.Goal              `json:"goal"`
	PlannerStatus   string                         `json:"planner_status"`
	Diagnostics     []researchplan.Diagnostic      `json:"diagnostics"`
	Markets         []researchplan.MarketSelection `json:"markets"`
	State           string                         `json:"state"`
	Phase           string                         `json:"phase"`
	WaitingReason   string                         `json:"waiting_reason"`
	Outcome         string                         `json:"outcome"`
	StopReason      string                         `json:"stop_reason"`
	Counters        Counters                       `json:"counters"`
	Members         []Member                       `json:"members"`
	Actions         []ActionStatus                 `json:"actions"`
	Firecrawl       FirecrawlBudget                `json:"firecrawl"`
	CreatedAt       time.Time                      `json:"created_at"`
	UpdatedAt       time.Time                      `json:"updated_at"`
	HeartbeatAt     time.Time                      `json:"heartbeat_at"`
	LastProgressAt  time.Time                      `json:"last_progress_at"`
	CompletedAt     *time.Time                     `json:"completed_at,omitempty"`
}
type Port interface {
	Start(context.Context, StartRequest) (Snapshot, error)
	Get(context.Context, Scope, string) (Snapshot, error)
	FindByIntent(context.Context, Scope, string) ([]Snapshot, error)
	Control(context.Context, ControlRequest) (Snapshot, error)
}

// HealthChecker проверяет действующий worker и его хранилище, без вызовов
// платных провайдеров и без подмены наличием конфигурации.
type HealthChecker interface {
	CheckHealth(context.Context) error
}
