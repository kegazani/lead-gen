package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/app/command"
	"lead-gen/backend/contexts/data-governance/internal/app/query"
	"lead-gen/backend/contexts/data-governance/internal/domain"
)

// createBasisBody — тело команды создания основания обработки.
type createBasisBody struct {
	BasisID       string `json:"basis_id"`
	TenantID      string `json:"tenant_id"`
	Purpose       string `json:"purpose"`
	Category      string `json:"category"`
	RetentionDays int    `json:"retention_days"`
	ValidFrom     string `json:"valid_from,omitempty"`
	ValidUntil    string `json:"valid_until,omitempty"`
	Actor         string `json:"actor"`
	Reason        string `json:"reason,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// changeAuthorityBody — тело команды изменения основания.
type changeAuthorityBody struct {
	TenantID      string `json:"tenant_id"`
	RetentionDays int    `json:"retention_days"`
	ValidFrom     string `json:"valid_from,omitempty"`
	ValidUntil    string `json:"valid_until,omitempty"`
	Actor         string `json:"actor"`
	Reason        string `json:"reason"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// revokeBody — тело команды отзыва основания.
type revokeBody struct {
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	Reason        string `json:"reason"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// suppressBody — тело команды внесения контакта в стоп-лист.
type suppressBody struct {
	TenantID      string `json:"tenant_id"`
	SuppressionID string `json:"suppression_id"`
	Contact       string `json:"contact"`
	Reason        string `json:"reason"`
	Actor         string `json:"actor"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// liftBody — тело команды снятия записи стоп-листа.
type liftBody struct {
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	Reason        string `json:"reason"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// basisResponse — единая форма ответа команды над основанием.
type basisResponse struct {
	BasisID string             `json:"basis_id"`
	Status  domain.BasisStatus `json:"status"`
	Version int                `json:"version"`
	Events  []string           `json:"events,omitempty"`
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCreateBasis обслуживает POST /bases.
func (s *Server) handleCreateBasis(w http.ResponseWriter, r *http.Request) {
	var body createBasisBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	validFrom, err := parseTime(body.ValidFrom)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	validUntil, err := parseTime(body.ValidUntil)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	tenant := domain.TenantID(body.TenantID)
	actor := domain.ActorID(body.Actor)
	if err := s.authorize(r, tenant, actor); err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := s.deps.CreateBasis.Handle(r.Context(), command.CreateBasis{
		BasisID:       domain.BasisID(body.BasisID),
		TenantID:      tenant,
		Purpose:       body.Purpose,
		Category:      domain.DataCategory(body.Category),
		RetentionDays: body.RetentionDays,
		ValidFrom:     validFrom,
		ValidUntil:    validUntil,
		Actor:         actor,
		Reason:        body.Reason,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toBasisResponse(result))
}

// handleGetBasis обслуживает GET /bases/{basisID}.
func (s *Server) handleGetBasis(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetBasis.Handle(r.Context(), query.GetBasis{
		BasisID:  domain.BasisID(r.PathValue("basisID")),
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleChangeAuthority обслуживает POST /bases/{basisID}/authority.
func (s *Server) handleChangeAuthority(w http.ResponseWriter, r *http.Request) {
	var body changeAuthorityBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	validFrom, err := parseTime(body.ValidFrom)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	validUntil, err := parseTime(body.ValidUntil)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	tenant := domain.TenantID(body.TenantID)
	actor := domain.ActorID(body.Actor)
	if err := s.authorize(r, tenant, actor); err != nil {
		writeDomainError(w, err)
		return
	}
	result, err := s.deps.ChangeAuthority.Handle(r.Context(), command.ChangeAuthority{
		BasisID:       domain.BasisID(r.PathValue("basisID")),
		TenantID:      tenant,
		RetentionDays: body.RetentionDays,
		ValidFrom:     validFrom,
		ValidUntil:    validUntil,
		Actor:         actor,
		Reason:        body.Reason,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBasisResponse(result))
}

// handleRevokeBasis обслуживает POST /bases/{basisID}/revocation.
func (s *Server) handleRevokeBasis(w http.ResponseWriter, r *http.Request) {
	var body revokeBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	tenant := domain.TenantID(body.TenantID)
	actor := domain.ActorID(body.Actor)
	if err := s.authorize(r, tenant, actor); err != nil {
		writeDomainError(w, err)
		return
	}
	result, err := s.deps.RevokeBasis.Handle(r.Context(), command.RevokeBasis{
		BasisID:       domain.BasisID(r.PathValue("basisID")),
		TenantID:      tenant,
		Actor:         actor,
		Reason:        body.Reason,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBasisResponse(result))
}

// handleSuppressContact обслуживает POST /bases/{basisID}/suppressions.
func (s *Server) handleSuppressContact(w http.ResponseWriter, r *http.Request) {
	var body suppressBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	tenant := domain.TenantID(body.TenantID)
	actor := domain.ActorID(body.Actor)
	if err := s.authorize(r, tenant, actor); err != nil {
		writeDomainError(w, err)
		return
	}
	result, err := s.deps.SuppressContact.Handle(r.Context(), command.SuppressContact{
		BasisID:       domain.BasisID(r.PathValue("basisID")),
		TenantID:      tenant,
		SuppressionID: domain.SuppressionID(body.SuppressionID),
		Contact:       domain.ContactRef(body.Contact),
		Reason:        body.Reason,
		Actor:         actor,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBasisResponse(result))
}

// handleLiftSuppression обслуживает
// POST /bases/{basisID}/suppressions/{suppressionID}/lift.
func (s *Server) handleLiftSuppression(w http.ResponseWriter, r *http.Request) {
	var body liftBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	tenant := domain.TenantID(body.TenantID)
	actor := domain.ActorID(body.Actor)
	if err := s.authorize(r, tenant, actor); err != nil {
		writeDomainError(w, err)
		return
	}
	result, err := s.deps.LiftSuppression.Handle(r.Context(), command.LiftSuppression{
		BasisID:       domain.BasisID(r.PathValue("basisID")),
		TenantID:      tenant,
		SuppressionID: domain.SuppressionID(r.PathValue("suppressionID")),
		Actor:         actor,
		Reason:        body.Reason,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBasisResponse(result))
}

// handleCheckZone обслуживает GET /zone-policy.
func (s *Server) handleCheckZone(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.CheckZone.Handle(r.Context(), query.CheckZone{
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
		Purpose:  r.URL.Query().Get("purpose"),
		Category: domain.DataCategory(r.URL.Query().Get("category")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleCheckSuppression обслуживает GET /suppressions/check.
func (s *Server) handleCheckSuppression(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.CheckSuppress.Handle(r.Context(), query.CheckSuppression{
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
		Contact:  domain.ContactRef(r.URL.Query().Get("contact")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// parseTime разбирает момент в формате RFC3339. Пустое значение — нулевое время.
func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("разбор времени %q: %w", value, err)
	}
	return at, nil
}

// toBasisResponse переводит результат команды в форму ответа.
func toBasisResponse(result command.BasisResult) basisResponse {
	response := basisResponse{
		BasisID: result.BasisID.String(),
		Status:  result.Status,
		Version: result.Version,
	}
	for _, event := range result.Events {
		response.Events = append(response.Events, string(event.Name()))
	}
	return response
}
