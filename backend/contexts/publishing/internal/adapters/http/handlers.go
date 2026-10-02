package httpapi

import (
	"net/http"
	"strings"

	"lead-gen/backend/contexts/publishing/internal/app/command"
	"lead-gen/backend/contexts/publishing/internal/app/query"
	"lead-gen/backend/contexts/publishing/internal/domain"
)

// healthResponse — ответ проверки живости сервиса.
type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// serviceName — идентификатор сервиса, равный идентификатору контекста.
const serviceName = "publishing"

// handleHealth отвечает на /healthz. Роль api подтверждает, что процесс поднят и
// принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Service: serviceName})
}

// targetRequest — один материал HTTP-команды.
type targetRequest struct {
	PublicationID  string `json:"publication_id"`
	Platform       string `json:"platform"`
	Account        string `json:"account"`
	IdempotencyKey string `json:"idempotency_key"`
	Title          string `json:"title"`
	Body           string `json:"body"`
}

// requestPublicationRequest — тело команды на публикацию.
type requestPublicationRequest struct {
	TenantID      string          `json:"tenant_id"`
	Actor         string          `json:"actor"`
	DigestID      string          `json:"digest_id"`
	CommunityID   string          `json:"community_id"`
	Period        string          `json:"period"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id"`
	Targets       []targetRequest `json:"targets"`
}

// publicationStateResponse — состояние публикации в ответе.
type publicationStateResponse struct {
	PublicationID string `json:"publication_id"`
	Platform      string `json:"platform"`
	Account       string `json:"account,omitempty"`
	Status        string `json:"status"`
	Shortened     bool   `json:"shortened,omitempty"`
	Address       string `json:"address,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	Title         string `json:"title,omitempty"`
	Body          string `json:"body,omitempty"`
	ApprovedBy    string `json:"approved_by,omitempty"`
}

// requestPublicationResponse — ответ на команду публикации.
type requestPublicationResponse struct {
	Publications []publicationStateResponse `json:"publications"`
}

// handleRequestPublication принимает контракт publication-command по HTTP.
func (s *Server) handleRequestPublication(w http.ResponseWriter, r *http.Request) {
	var body requestPublicationRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	targets := make([]command.PublicationRequest, 0, len(body.Targets))
	for _, target := range body.Targets {
		parsed, err := domain.NewPublicationTarget(target.Platform, target.Account)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		targets = append(targets, command.PublicationRequest{
			PublicationID:  domain.PublicationID(target.PublicationID),
			Target:         parsed,
			Title:          target.Title,
			Body:           target.Body,
			IdempotencyKey: domain.IdempotencyKey(target.IdempotencyKey),
		})
	}
	result, err := s.deps.RequestPublication.Handle(r.Context(), command.RequestPublication{
		TenantID:      domain.TenantID(body.TenantID),
		Actor:         domain.ActorID(body.Actor),
		Digest:        domain.DigestRef{ID: body.DigestID, CommunityID: body.CommunityID, Period: body.Period},
		Targets:       targets,
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	response := requestPublicationResponse{Publications: make([]publicationStateResponse, 0, len(result.Publications))}
	for _, state := range result.Publications {
		response.Publications = append(response.Publications, publicationStateResponse{
			PublicationID: state.ID.String(),
			Platform:      string(state.Target.Platform()),
			Account:       state.Target.Account(),
			Status:        string(state.Status),
			Shortened:     state.Shortened,
		})
	}
	writeJSON(w, http.StatusCreated, response)
}

// approvalRequest — тело утверждения и отклонения.
type approvalRequest struct {
	TenantID string `json:"tenant_id"`
	Actor    string `json:"actor"`
	Reason   string `json:"reason,omitempty"`
}

// handleApprove утверждает материал к публикации.
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	var body approvalRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	state, err := s.deps.Approve.Handle(r.Context(), command.Approve{
		PublicationID: domain.PublicationID(r.PathValue("publicationID")),
		TenantID:      domain.TenantID(body.TenantID),
		Actor:         domain.ActorID(body.Actor),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stateResponse(state))
}

// handleReject отклоняет материал.
func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	var body approvalRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	state, err := s.deps.Reject.Handle(r.Context(), command.Reject{
		PublicationID: domain.PublicationID(r.PathValue("publicationID")),
		TenantID:      domain.TenantID(body.TenantID),
		Actor:         domain.ActorID(body.Actor),
		Reason:        body.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stateResponse(state))
}

// publishRequest — тело команды исполнения публикации.
type publishRequest struct {
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	CorrelationID string `json:"correlation_id"`
	CausationID   string `json:"causation_id"`
}

// handlePublish исполняет публикацию через порт площадки.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var body publishRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	result, err := s.deps.Publish.Handle(r.Context(), command.Publish{
		PublicationID: domain.PublicationID(r.PathValue("publicationID")),
		TenantID:      domain.TenantID(body.TenantID),
		Actor:         domain.ActorID(body.Actor),
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	response := stateResponse(result.State)
	response.Address = result.Address
	response.FailureReason = result.FailureReason
	writeJSON(w, http.StatusOK, response)
}

// handleGetPublication читает публикацию по идентификатору.
func (s *Server) handleGetPublication(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetPublication.Handle(r.Context(), query.GetPublication{
		PublicationID: domain.PublicationID(r.PathValue("publicationID")),
		TenantID:      domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewResponse(view))
}

// handlePublicationQueue читает очередь публикаций по статусу.
func (s *Server) handlePublicationQueue(w http.ResponseWriter, r *http.Request) {
	views, err := s.deps.PublicationQueue.Handle(r.Context(), query.PublicationQueue{
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
		Status:   domain.PublicationStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	response := requestPublicationResponse{Publications: make([]publicationStateResponse, 0, len(views))}
	for _, view := range views {
		response.Publications = append(response.Publications, viewResponse(view))
	}
	writeJSON(w, http.StatusOK, response)
}

// stateResponse переводит состояние команды в ответ.
func stateResponse(state command.PublicationState) publicationStateResponse {
	return publicationStateResponse{
		PublicationID: state.ID.String(),
		Platform:      string(state.Target.Platform()),
		Account:       state.Target.Account(),
		Status:        string(state.Status),
		Shortened:     state.Shortened,
	}
}

// viewResponse переводит проекцию чтения в ответ.
func viewResponse(view query.PublicationView) publicationStateResponse {
	return publicationStateResponse{
		PublicationID: view.ID.String(),
		Platform:      string(view.Platform),
		Account:       view.Account,
		Status:        string(view.Status),
		Shortened:     view.Shortened,
		Address:       view.Address,
		FailureReason: view.FailureReason,
		Title:         view.Title,
		Body:          view.Body,
		ApprovedBy:    view.ApprovedBy.String(),
	}
}
