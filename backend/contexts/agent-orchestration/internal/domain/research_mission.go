package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	rm "lead-gen/contracts/agent-orchestration/researchmission"
)

// ResearchMission хранится в клиентской red-зоне: Plan содержит исходный ICP,
// поисковые строки, а действия — URL и receipts. В green эти данные не публикуются.
type ResearchMission struct {
	Request        rm.StartRequest    `json:"request"`
	ID             string             `json:"id"`
	InputHash      string             `json:"input_hash"`
	Revision       int64              `json:"revision"`
	State          string             `json:"state"`
	Phase          string             `json:"phase"`
	WaitingReason  string             `json:"waiting_reason"`
	Outcome        string             `json:"outcome"`
	StopReason     string             `json:"stop_reason"`
	Actions        []ResearchAction   `json:"actions"`
	Members        []rm.Member        `json:"members"`
	Discovered     map[string]bool    `json:"discovered"`
	Documents      int                `json:"documents"`
	LateUpdates    int                `json:"late_updates"`
	LastQueue      string             `json:"last_queue"`
	Firecrawl      rm.FirecrawlBudget `json:"firecrawl"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	HeartbeatAt    time.Time          `json:"heartbeat_at"`
	LastProgressAt time.Time          `json:"last_progress_at"`
	CompletedAt    *time.Time         `json:"completed_at,omitempty"`
	FinalMembers   []rm.Member        `json:"final_members,omitempty"`
	FinalCounters  *rm.Counters       `json:"final_counters,omitempty"`
}

type ResearchAction struct {
	SettlementState      string          `json:"settlement_state,omitempty"`
	SettlementReason     string          `json:"settlement_reason,omitempty"`
	SettlementEpoch      int64           `json:"settlement_epoch,omitempty"`
	SettlementLeaseUntil *time.Time      `json:"settlement_lease_until,omitempty"`
	SettlementNextAt     *time.Time      `json:"settlement_next_at,omitempty"`
	SettlementReceipt    json.RawMessage `json:"settlement_receipt,omitempty"`
	SettlementInput      json.RawMessage `json:"settlement_input,omitempty"`
	AnalysisDiagnostics  []string        `json:"analysis_diagnostics,omitempty"`
	DiscoverySourceID    string          `json:"discovery_source_id,omitempty"`
	ID                   string          `json:"id"`
	Kind                 string          `json:"kind"`
	Queue                string          `json:"queue"`
	SourceID             string          `json:"source_id"`
	StrategyCellID       string          `json:"strategy_cell_id"`
	QueryFamilyID        string          `json:"query_family_id"`
	Country              string          `json:"country"`
	Language             string          `json:"language"`
	ProfileRevision      string          `json:"profile_revision"`
	Query                string          `json:"query,omitempty"`
	URL                  string          `json:"url,omitempty"`
	ParentID             string          `json:"parent_id,omitempty"`
	Depth                int             `json:"depth"`
	State                string          `json:"state"`
	Attempt              int             `json:"attempt"`
	Epoch                int64           `json:"epoch"`
	LeaseUntil           *time.Time      `json:"lease_until,omitempty"`
	NextAttemptAt        *time.Time      `json:"next_attempt_at,omitempty"`
	Reason               string          `json:"reason,omitempty"`
	Receipt              json.RawMessage `json:"receipt,omitempty"`
	ReceiptHash          string          `json:"receipt_hash,omitempty"`
	Analysis             json.RawMessage `json:"analysis,omitempty"`
	Ingested             []string        `json:"ingested,omitempty"`
	AnalysisAttempts     int             `json:"analysis_attempts"`
}

func ResearchID(parts ...string) string {
	raw, _ := json.Marshal(parts)
	hash := sha256.Sum256(raw)
	return "research-" + hex.EncodeToString(hash[:16])
}

func NewResearchMission(req rm.StartRequest, now time.Time) (*ResearchMission, error) {
	if req.Scope.Validate() != nil || req.Limits.Validate() != nil || req.IntentID == "" || req.Actor == "" || req.Generation == "" || req.CriteriaVersion < 1 {
		return nil, rm.ErrInvalid
	}
	if err := req.Plan.Validate(); err != nil {
		return nil, fmt.Errorf("%w: brief: %v", rm.ErrInvalid, err)
	}
	brief := req.Plan.Brief
	if brief.TenantID != req.Scope.TenantID || brief.Purpose != req.Scope.PurposeID || brief.RequestID != req.IntentID || brief.ActorID != req.Actor || brief.Criteria.Version != req.CriteriaVersion || req.Plan.TenantID != req.Scope.TenantID || req.Plan.Purpose != req.Scope.PurposeID || req.Plan.BriefID != brief.ID || req.Plan.BriefRevision != brief.Revision || !req.Limits.Deadline.After(now) {
		return nil, rm.ErrInvalid
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(raw)
	m := &ResearchMission{Request: req, ID: ResearchID(req.Scope.TenantID, req.Scope.PurposeID, req.IntentID, fmt.Sprint(req.CriteriaVersion), req.Generation), InputHash: hex.EncodeToString(h[:]), Revision: 1, State: "queued", Phase: "discovery", CreatedAt: now, UpdatedAt: now, HeartbeatAt: now, LastProgressAt: now, Discovered: map[string]bool{}, Firecrawl: rm.FirecrawlBudget{GrantRef: req.FirecrawlGrantRef}}
	families := map[string]int{}
	for i, f := range req.Plan.Families {
		if _, ok := families[f.ID]; ok || f.ID == "" || strings.TrimSpace(f.Query) == "" {
			return nil, rm.ErrInvalid
		}
		families[f.ID] = i
	}
	seen := map[string]bool{}
	for _, cell := range req.Plan.Cells {
		index, ok := families[cell.QueryFamilyID]
		if !ok || cell.SourceID == "" || cell.SourceRevision == "" {
			return nil, rm.ErrInvalid
		}
		f := req.Plan.Families[index]
		if cell.Country != f.Country || cell.Language != f.Language || cell.SourceFamily != f.SourceFamily {
			return nil, rm.ErrInvalid
		}
		allowed := false
		for _, market := range brief.Markets {
			if market.Country == cell.Country {
				for _, language := range market.Languages {
					if language == cell.Language {
						allowed = true
					}
				}
			}
		}
		if !allowed {
			return nil, rm.ErrInvalid
		}
		words := strings.Fields(strings.ToLower(f.Query))
		sort.Strings(words)
		id := ResearchID(m.ID, "search", cell.SourceID, cell.Country, cell.Language, strings.Join(words, " "))
		if seen[id] {
			continue
		}
		seen[id] = true
		if len(m.Actions) >= req.Limits.MaxActions {
			return nil, fmt.Errorf("%w: портфель превышает max_actions", rm.ErrInvalid)
		}
		m.Actions = append(m.Actions, ResearchAction{ID: id, Kind: "search", Queue: "discovery", SourceID: cell.SourceID, StrategyCellID: cell.ID, QueryFamilyID: f.ID, Country: cell.Country, Language: cell.Language, ProfileRevision: cell.SourceRevision, Query: f.Query, State: "pending", Attempt: 1})
	}
	if len(m.Actions) == 0 {
		return nil, fmt.Errorf("%w: нет доступных стратегий", rm.ErrInvalid)
	}
	return m, nil
}

func (m *ResearchMission) Terminal() bool {
	return m.State == "completed" || m.State == "cancelled" || m.State == "failed"
}
func (m *ResearchMission) Touch(now time.Time) { m.Revision++; m.UpdatedAt = now }
func (m *ResearchMission) Action(id string) *ResearchAction {
	for i := range m.Actions {
		if m.Actions[i].ID == id {
			return &m.Actions[i]
		}
	}
	return nil
}
func (m *ResearchMission) Counters() rm.Counters {
	c := rm.Counters{Discovered: len(m.Discovered), Documents: m.Documents, Saved: len(m.VisibleMembers()), LateUpdates: m.LateUpdates}
	for _, member := range m.VisibleMembers() {
		if member.Accepted {
			c.Accepted++
		}
		if member.HasContact {
			c.ContactReady++
		}
		if member.EmailReady {
			c.EmailReady++
		}
	}
	return c
}
func (m *ResearchMission) Snapshot() rm.Snapshot {
	members := m.VisibleMembers()
	counters := m.Counters()
	if m.FinalCounters != nil {
		counters = *m.FinalCounters
		counters.LateUpdates = m.LateUpdates
		members = append([]rm.Member{}, m.FinalMembers...)
	}
	actions := make([]rm.ActionStatus, 0, len(m.Actions))
	warnings := []rm.Warning{}
	for _, a := range m.Actions {
		if a.SettlementState == "pending" || a.SettlementState == "running" || a.SettlementState == "quarantined" || a.SettlementState == "settled-unknown" {
			warnings = append(warnings, rm.Warning{Code: a.SettlementReason, ActionID: a.ID, SourceID: a.SourceID, Country: a.Country, Language: a.Language})
		}
		for _, code := range a.AnalysisDiagnostics {
			warnings = append(warnings, rm.Warning{Code: code, ActionID: a.ID, SourceID: a.SourceID, Country: a.Country, Language: a.Language})
		}
		actions = append(actions, rm.ActionStatus{SettlementState: a.SettlementState, SettlementReason: a.SettlementReason, SettlementNextAt: a.SettlementNextAt, Diagnostics: append([]string(nil), a.AnalysisDiagnostics...), ID: a.ID, Kind: a.Kind, Queue: a.Queue, SourceID: a.SourceID, StrategyCellID: a.StrategyCellID, Country: a.Country, Language: a.Language, State: a.State, Attempt: a.Attempt, Reason: a.Reason, NextAttemptAt: a.NextAttemptAt})
	}
	for _, member := range members {
		if member.Reason == "identity-review-required" {
			warning := rm.Warning{Code: member.Reason, ActionID: member.ActionID, EntityID: member.EntityID}
			if action := m.Action(member.ActionID); action != nil {
				warning.SourceID = action.SourceID
				warning.Country = action.Country
				warning.Language = action.Language
			}
			warnings = append(warnings, warning)
		}
	}
	return rm.Snapshot{Warnings: warnings, Scope: m.Request.Scope, MissionID: m.ID, IntentID: m.Request.IntentID, Revision: m.Revision, CriteriaVersion: m.Request.CriteriaVersion, Goal: m.Request.Plan.Brief.Goal, PlannerStatus: m.Request.Plan.PlannerStatus, Diagnostics: m.Request.Plan.Diagnostics, Markets: m.Request.Plan.Brief.Markets, State: m.State, Phase: m.Phase, WaitingReason: m.WaitingReason, Outcome: m.Outcome, StopReason: m.StopReason, Counters: counters, Members: members, Actions: actions, Firecrawl: m.Firecrawl, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, HeartbeatAt: m.HeartbeatAt, LastProgressAt: m.LastProgressAt, CompletedAt: m.CompletedAt}
}
func (m *ResearchMission) Finish(state, reason string, now time.Time) {
	if m.Terminal() {
		return
	}
	m.State = state
	m.Phase = "finished"
	m.StopReason = reason
	m.WaitingReason = ""
	m.Outcome = "partial"
	if m.Counters().Accepted >= m.Request.Plan.Brief.Goal.Quantity {
		m.Outcome = "goal-reached"
	}
	if state == "cancelled" {
		m.Outcome = "cancelled"
	}
	m.CompletedAt = &now
	c := m.Counters()
	m.FinalCounters = &c
	m.FinalMembers = m.VisibleMembers()
	for i := range m.Actions {
		if m.Actions[i].State == "pending" {
			m.Actions[i].State = "cancelled"
			m.Actions[i].Reason = reason
		}
	}
}
func (m *ResearchMission) Control(req rm.ControlRequest, now time.Time) error {
	if req.ExpectedRevision != m.Revision {
		return rm.ErrConflict
	}
	if req.Actor == "" || m.Terminal() {
		return rm.ErrInvalid
	}
	switch req.Command {
	case "pause":
		if m.State != "running" && m.State != "queued" {
			return rm.ErrInvalid
		}
		if strings.TrimSpace(req.Reason) == "" {
			return rm.ErrInvalid
		}
		m.State = "paused"
		m.WaitingReason = req.Reason
	case "resume":
		if m.State != "paused" {
			return rm.ErrInvalid
		}
		if !now.Before(m.Request.Limits.Deadline) {
			m.Finish("completed", "technical-deadline", now)
		} else {
			m.State = "running"
			m.WaitingReason = ""
		}
	case "cancel":
		m.Finish("cancelled", "human-cancelled", now)
	default:
		return rm.ErrInvalid
	}
	m.Touch(now)
	return nil
}

// VisibleMembers группирует только явную identity-проекцию владельца. Исходные
// refs/оценки остаются в Members: отзыв связи может снова разделить группу.
func (m *ResearchMission) VisibleMembers() []rm.Member {
	out := []rm.Member{}
	indexes := map[string]int{}
	for _, member := range m.Members {
		canonical := member.CanonicalEntityID
		if canonical == "" {
			canonical = member.EntityID
		}
		index, exists := indexes[canonical]
		if !exists {
			copy := member
			copy.EntityID = canonical
			copy.CanonicalEntityID = canonical
			copy.CriteriaAccepted = member.CriteriaAccepted && !member.SourceUnavailable
			copy.OriginalEntityIDs = []string{member.EntityID}
			indexes[canonical] = len(out)
			out = append(out, copy)
			continue
		}
		group := &out[index]
		group.OriginalEntityIDs = append(group.OriginalEntityIDs, member.EntityID)
		group.DistinctCanonicalEntityIDs = append(group.DistinctCanonicalEntityIDs, member.DistinctCanonicalEntityIDs...)
		group.HasContact = group.HasContact || member.HasContact
		group.HasEmail = group.HasEmail || member.HasEmail
		group.EmailReady = group.EmailReady || member.EmailReady
		group.CriteriaAccepted = group.CriteriaAccepted || (member.CriteriaAccepted && !member.SourceUnavailable)
		group.SourceUnavailable = group.SourceUnavailable && member.SourceUnavailable
		if member.IdentityRevision > group.IdentityRevision {
			group.IdentityRevision = member.IdentityRevision
		}
		if member.Accepted {
			group.Accepted = true
			group.State = member.State
			group.Reason = member.Reason
			group.ReceiptID = member.ReceiptID
			group.EvidenceRefs = member.EvidenceRefs
			group.EntityRevision = member.EntityRevision
		}
	}
	return out
}

func (m *ResearchMission) Recredit() {
	if m.Terminal() {
		return
	}
	for i := range m.Members {
		m.Members[i].Accepted = false
		if m.Members[i].SourceUnavailable {
			m.Members[i].State = "unavailable"
			m.Members[i].Reason = "owner-evidence-withdrawn"
		} else if m.Members[i].CriteriaAccepted {
			m.Members[i].State = "pending-readiness"
		} else {
			m.Members[i].State = "pending-criteria"
			if m.Members[i].Reason == "criteria-review-required" {
				m.Members[i].State = "criteria-review-required"
			}
		}
	}
	groups := m.VisibleMembers()
	accepted := 0
	creditedGroups := []rm.Member{}
	for _, group := range groups {
		eligible := group.CriteriaAccepted && !group.SourceUnavailable
		switch m.Request.Plan.Brief.Goal.Unit {
		case "company-with-contact":
			eligible = eligible && group.HasContact
		case "email-ready":
			eligible = eligible && group.EmailReady && group.HasEmail
		}
		if !eligible {
			continue
		}
		duplicateReview := false
		for _, credited := range creditedGroups {
			if m.sameIdentityName(group.CanonicalEntityID, credited.CanonicalEntityID) && !knownDistinct(group, credited) {
				duplicateReview = true
				break
			}
		}
		if duplicateReview {
			for i := range m.Members {
				member := &m.Members[i]
				canonical := member.CanonicalEntityID
				if canonical == "" {
					canonical = member.EntityID
				}
				if canonical == group.CanonicalEntityID {
					member.State = "identity-review-required"
					member.Reason = "identity-review-required"
				}
			}
			continue
		}
		for i := range m.Members {
			member := &m.Members[i]
			canonical := member.CanonicalEntityID
			if canonical == "" {
				canonical = member.EntityID
			}
			if canonical != group.CanonicalEntityID || !member.CriteriaAccepted || member.SourceUnavailable {
				continue
			}
			member.State = "eligible-surplus"
			if accepted < m.Request.Plan.Brief.Goal.Quantity {
				member.Accepted = true
				member.State = "accepted"
				if member.Reason == "identity-review-required" {
					member.Reason = "criteria-evidence-accepted"
				}
				accepted++
				creditedGroups = append(creditedGroups, group)
			}
			break
		}
	}
}

// Совпадение имени создаёт только удержание повторного зачёта. Оно не меняет
// тождество и не переносит контакты между различными canonical refs.
func identityNameKey(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, name)
	return strings.Join(strings.Fields(name), " ")
}
func (m *ResearchMission) sameIdentityName(left, right string) bool {
	names := map[string]bool{}
	for _, member := range m.Members {
		canonical := member.CanonicalEntityID
		if canonical == "" {
			canonical = member.EntityID
		}
		if canonical == left && !member.SourceUnavailable {
			if key := identityNameKey(member.Name); key != "" {
				names[key] = true
			}
		}
	}
	for _, member := range m.Members {
		canonical := member.CanonicalEntityID
		if canonical == "" {
			canonical = member.EntityID
		}
		if canonical == right && !member.SourceUnavailable && names[identityNameKey(member.Name)] {
			return true
		}
	}
	return false
}
func knownDistinct(left, right rm.Member) bool {
	for _, id := range left.DistinctCanonicalEntityIDs {
		if id == right.CanonicalEntityID {
			return true
		}
	}
	for _, id := range right.DistinctCanonicalEntityIDs {
		if id == left.CanonicalEntityID {
			return true
		}
	}
	return false
}
