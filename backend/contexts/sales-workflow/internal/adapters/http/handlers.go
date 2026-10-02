package httpapi

import (
	"net/http"

	"lead-gen/backend/contexts/sales-workflow/internal/app/command"
	"lead-gen/backend/contexts/sales-workflow/internal/app/query"
	"lead-gen/backend/contexts/sales-workflow/internal/domain"
)

// createDealBody — тело команды создания сделки.
type createDealBody struct {
	DealID           string `json:"deal_id"`
	TenantID         string `json:"tenant_id"`
	CompanyRef       string `json:"company_ref"`
	ExternalRef      string `json:"external_ref"`
	ExternalRevision int64  `json:"external_revision,omitempty"`
	Actor            string `json:"actor"`
	CorrelationID    string `json:"correlation_id,omitempty"`
	CausationID      string `json:"causation_id,omitempty"`
}

// changeStageBody — тело команды перехода этапа.
type changeStageBody struct {
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	To            string `json:"to"`
	Reason        string `json:"reason,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// attributeBody — тело команды подтверждения результата.
type attributeBody struct {
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	ActionRef     string `json:"action_ref"`
	EvidenceRef   string `json:"evidence_ref"`
	ExternalRef   string `json:"external_ref"`
	Reason        string `json:"reason,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// dealResponse — форма ответа команды над сделкой.
type dealResponse struct {
	DealID          string           `json:"deal_id"`
	Stage           domain.DealStage `json:"stage"`
	Version         int              `json:"version"`
	ResultConfirmed bool             `json:"result_confirmed"`
}

// handleCreateDeal обслуживает POST /deals.
func (s *Server) handleCreateDeal(w http.ResponseWriter, r *http.Request) {
	var body createDealBody
	if err := decodeJSON(w, r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	revision := body.ExternalRevision
	if revision == 0 {
		// Ревизия по умолчанию: заведение сделки означает первую запись в CRM.
		revision = 1
	}
	result, err := s.deps.CreateDeal.Handle(r.Context(), command.CreateDeal{
		DealID:           domain.DealID(body.DealID),
		TenantID:         domain.TenantID(body.TenantID),
		CompanyRef:       domain.CompanyRef(body.CompanyRef),
		ExternalRef:      domain.ExternalDealRef(body.ExternalRef),
		ExternalRevision: revision,
		Actor:            domain.ActorID(body.Actor),
		CorrelationID:    domain.CorrelationID(body.CorrelationID),
		CausationID:      domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, dealResponse{
		DealID:  result.DealID.String(),
		Stage:   result.Stage,
		Version: result.Version,
	})
}

// handleGetDeal обслуживает GET /deals/{dealID}.
func (s *Server) handleGetDeal(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetDeal.Handle(r.Context(), query.GetDeal{
		DealID:   domain.DealID(r.PathValue("dealID")),
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// handleChangeStage обслуживает POST /deals/{dealID}/stage.
func (s *Server) handleChangeStage(w http.ResponseWriter, r *http.Request) {
	var body changeStageBody
	if err := decodeJSON(w, r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.ChangeDealStage.Handle(r.Context(), command.ChangeDealStage{
		DealID:        domain.DealID(r.PathValue("dealID")),
		TenantID:      domain.TenantID(body.TenantID),
		Actor:         domain.ActorID(body.Actor),
		To:            domain.DealStage(body.To),
		Reason:        body.Reason,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, dealResponse{
		DealID:  result.DealID.String(),
		Stage:   result.Stage,
		Version: result.Version,
	})
}

// handleAttributeResult обслуживает POST /deals/{dealID}/attribution.
func (s *Server) handleAttributeResult(w http.ResponseWriter, r *http.Request) {
	var body attributeBody
	if err := decodeJSON(w, r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.AttributeResult.Handle(r.Context(), command.AttributeResult{
		DealID:        domain.DealID(r.PathValue("dealID")),
		TenantID:      domain.TenantID(body.TenantID),
		Actor:         domain.ActorID(body.Actor),
		ActionRef:     domain.ActionRef(body.ActionRef),
		EvidenceRef:   domain.EvidenceRef(body.EvidenceRef),
		ExternalRef:   domain.ExternalDealRef(body.ExternalRef),
		Reason:        body.Reason,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, dealResponse{
		DealID:          result.DealID.String(),
		Stage:           result.Stage,
		Version:         result.Version,
		ResultConfirmed: true,
	})
}

// handleCRMSyncStatus обслуживает GET /deals/{dealID}/crm-sync.
func (s *Server) handleCRMSyncStatus(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.CRMSyncStatus.Handle(r.Context(), query.CRMSyncStatus{
		DealID:   domain.DealID(r.PathValue("dealID")),
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
