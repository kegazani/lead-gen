// Package ingest реализует HTTP-адаптер роли ingest контекста sales-workflow:
// публичный приём событий внешней CRM на отдельном домене.
//
// Адаптер разбирает контракт crm-event, проверяет его форму и вызывает прикладной
// сценарий. Решений о допустимости он не принимает: инварианты обеспечивает
// aggregate root deal/crm-sync, политики conflict-resolution и
// confirmed-attribution применяет домен.
package ingest

import (
	"encoding/json"
	"errors"
	"net/http"

	httpapi "lead-gen/backend/contexts/sales-workflow/internal/adapters/http"
	"lead-gen/backend/contexts/sales-workflow/internal/app/command"
	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
	"lead-gen/contracts/sales-workflow/crmevent"
)

const (
	maxBodyBytes    = 1 << 20
	contentTypeJSON = "application/json; charset=utf-8"
)

// Dependencies — внешние зависимости адаптера приёма.
type Dependencies struct {
	Ingest *command.IngestCRMEventHandler
}

// Server — HTTP-сервер роли ingest. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
}

// NewServer собирает маршруты приёма.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps}
	server.handler = server.routes()
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты приёма.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /crm/events", s.handleCRMEvent)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	return mux
}

// errorResponse — единая форма ошибки HTTP-слоя.
type errorResponse struct {
	Error string `json:"error"`
}

// handleCRMEvent обслуживает POST /crm/events: принимает событие CRM и применяет
// его к сделке и синхронизации.
func (s *Server) handleCRMEvent(w http.ResponseWriter, r *http.Request) {
	var event crmevent.CRMEvent
	if r.Body == nil {
		httpapi.WriteError(w, http.StatusBadRequest, "тело запроса отсутствует")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := event.Validate(); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.deps.Ingest.Handle(r.Context(), command.IngestCRMEvent{
		DealID:           domain.DealID(event.DealID),
		TenantID:         domain.TenantID(event.TenantID),
		Actor:            domain.ActorID(event.Source),
		ExternalRef:      domain.ExternalDealRef(event.ExternalRef),
		ExternalRevision: event.ExternalRevision,
		Stage:            domain.DealStage(event.Stage),
		ResultConfirmed:  event.ResultConfirmed,
		ActionRef:        domain.ActionRef(event.ActionRef),
		EvidenceRef:      domain.EvidenceRef(event.EvidenceRef),
		At:               event.OccurredAt,
		CorrelationID:    domain.CorrelationID(event.CorrelationID),
		CausationID:      domain.CausationID(event.CausationID),
	})
	if err != nil {
		httpapi.WriteError(w, statusForError(err), errorText(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, ingestResponse{
		Applied:    result.Applied,
		SyncStatus: string(result.SyncStatus),
		Stage:      string(result.Stage),
		Version:    result.Version,
	})
}

// handleHealth обслуживает GET /healthz.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ingestResponse — форма ответа приёма.
type ingestResponse struct {
	Applied    bool   `json:"applied"`
	SyncStatus string `json:"sync_status"`
	Stage      string `json:"stage,omitempty"`
	Version    int    `json:"version,omitempty"`
}

// statusForError классифицирует ошибку приёма для кода ответа.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrDealNotFound),
		errors.Is(err, ports.ErrCRMSyncNotFound),
		errors.Is(err, command.ErrTenantMismatch):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrDealVersionConflict),
		errors.Is(err, domain.ErrInvalidStageTransition),
		errors.Is(err, domain.ErrTerminalDeal),
		errors.Is(err, domain.ErrAlreadyAttributed),
		errors.Is(err, domain.ErrAttributionNotAllowed):
		return http.StatusConflict
	case errors.Is(err, ports.ErrCRMUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrMissingEventID),
		errors.Is(err, domain.ErrUnknownDealStage),
		errors.Is(err, domain.ErrInvalidExternalVersion),
		errors.Is(err, domain.ErrInsufficientProof):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// errorText скрывает подробности внутренней ошибки от публичного приёма.
func errorText(err error) string {
	if statusForError(err) >= http.StatusInternalServerError {
		return "внутренняя ошибка"
	}
	return err.Error()
}
