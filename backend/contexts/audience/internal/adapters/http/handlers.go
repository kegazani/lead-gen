package httpapi

import (
	"net/http"
	"time"

	"lead-gen/backend/contexts/audience/internal/app/command"
	"lead-gen/backend/contexts/audience/internal/app/query"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports/contracts"
	"lead-gen/backend/platform/observability/observ"
)

// createSegmentRequest — тело POST /segments.
type createSegmentRequest struct {
	SegmentID       string   `json:"segment_id"`
	TenantID        string   `json:"tenant_id"`
	Name            string   `json:"name"`
	ModelVersion    string   `json:"model_version"`
	MinScore        float64  `json:"min_score"`
	RequiredSignals []string `json:"required_signals"`
	RequireAll      bool     `json:"require_all"`
}

// handleCreateSegment обслуживает POST /segments.
func (s *Server) handleCreateSegment(w http.ResponseWriter, r *http.Request) {
	var req createSegmentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.CreateSegment.Handle(r.Context(), command.CreateSegment{
		SegmentID:       domain.SegmentID(req.SegmentID),
		TenantID:        domain.TenantID(req.TenantID),
		Name:            req.Name,
		ModelVersion:    domain.ModelVersion(req.ModelVersion),
		MinScore:        req.MinScore,
		RequiredSignals: req.RequiredSignals,
		RequireAll:      req.RequireAll,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"segment_id": result.SegmentID.String(),
		"version":    result.Version,
	})
}

// scoreLeadRequest — тело POST /segments/{segmentID}/scores.
type scoreLeadRequest struct {
	LeadID        string                  `json:"lead_id"`
	TenantID      string                  `json:"tenant_id"`
	ObservedAt    time.Time               `json:"observed_at"`
	Signals       []contracts.SignalValue `json:"signals"`
	Actor         string                  `json:"actor"`
	CorrelationID string                  `json:"correlation_id"`
	CausationID   string                  `json:"causation_id"`
}

// handleScoreLead обслуживает POST /segments/{segmentID}/scores: скоринг лида и
// пересчёт его принадлежности сегменту.
func (s *Server) handleScoreLead(w http.ResponseWriter, r *http.Request) {
	var req scoreLeadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.ScoreLead.Handle(r.Context(), command.ScoreLead{
		SegmentID: domain.SegmentID(r.PathValue("segmentID")),
		TenantID:  domain.TenantID(req.TenantID),
		Profile: contracts.LeadProfile{
			LeadID:     req.LeadID,
			TenantID:   req.TenantID,
			ObservedAt: req.ObservedAt,
			Signals:    req.Signals,
		},
		Actor:         domain.ActorID(req.Actor),
		CorrelationID: domain.CorrelationID(req.CorrelationID),
		CausationID:   domain.CausationID(req.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"segment_id":         result.SegmentID.String(),
		"lead_id":            result.LeadID.String(),
		"score":              result.Value,
		"eligible":           result.Eligible,
		"membership_changed": result.MembershipChanged,
		"member_count":       result.MemberCount,
		"version":            result.Version,
	})
}

// handleSegmentMembers обслуживает GET /segments/{segmentID}/members.
func (s *Server) handleSegmentMembers(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	view, err := s.deps.SegmentMembers.Handle(r.Context(), query.SegmentMembers{
		SegmentID: domain.SegmentID(r.PathValue("segmentID")),
		TenantID:  domain.TenantID(tenant),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// fixSnapshotRequest — тело POST /audience-snapshots.
type fixSnapshotRequest struct {
	SnapshotID    string `json:"snapshot_id"`
	SegmentID     string `json:"segment_id"`
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	CorrelationID string `json:"correlation_id"`
	CausationID   string `json:"causation_id"`
}

// handleFixSnapshot обслуживает POST /audience-snapshots: фиксацию снимка.
func (s *Server) handleFixSnapshot(w http.ResponseWriter, r *http.Request) {
	var req fixSnapshotRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.FixSnapshot.Handle(r.Context(), command.FixAudienceSnapshot{
		SnapshotID:    domain.SnapshotID(req.SnapshotID),
		SegmentID:     domain.SegmentID(req.SegmentID),
		TenantID:      domain.TenantID(req.TenantID),
		Actor:         domain.ActorID(req.Actor),
		CorrelationID: domain.CorrelationID(req.CorrelationID),
		CausationID:   domain.CausationID(req.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"snapshot_id":  result.SnapshotID.String(),
		"segment_id":   result.SegmentID.String(),
		"member_count": result.MemberCount,
		"version":      result.Version,
	})
}

// audienceSnapshotResponse — проводная форма контракта audience-snapshot. Поля
// совпадают с конвертом и DTO потребителя (platform-контракт audience-snapshot).
type audienceSnapshotResponse struct {
	EventID       string    `json:"event_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	TenantID      string    `json:"tenant_id"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id,omitempty"`
	Name          string    `json:"name"`
	SchemaVersion string    `json:"schema_version"`
	SnapshotID    string    `json:"snapshot_id"`
	SegmentID     string    `json:"segment_id"`
	Version       int64     `json:"version"`
	MemberCount   int64     `json:"member_count"`
	GeneratedAt   time.Time `json:"generated_at"`
}

// handleGetSnapshot обслуживает GET /audience-snapshots/{snapshotID}. Ответ —
// контракт audience-snapshot; содержимое состава (ссылки на лидов) в проводную
// форму не входит: снимок разрешается на стороне владельца.
func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	view, err := s.deps.GetSnapshot.Handle(r.Context(), query.GetAudienceSnapshot{
		SnapshotID: domain.SnapshotID(r.PathValue("snapshotID")),
		TenantID:   domain.TenantID(tenant),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	correlation := view.SnapshotID
	if fields, ok := observ.FieldsFromContext(r.Context()); ok && fields.CorrelationID != "" {
		correlation = fields.CorrelationID
	}
	writeJSON(w, http.StatusOK, audienceSnapshotResponse{
		EventID:       view.SnapshotID,
		OccurredAt:    view.GeneratedAt,
		TenantID:      view.TenantID,
		CorrelationID: correlation,
		Name:          contractAudienceSnapshot,
		SchemaVersion: schemaVersionAudience,
		SnapshotID:    view.SnapshotID,
		SegmentID:     view.SegmentID,
		Version:       view.Version,
		MemberCount:   int64(view.MemberCount),
		GeneratedAt:   view.GeneratedAt,
	})
}
