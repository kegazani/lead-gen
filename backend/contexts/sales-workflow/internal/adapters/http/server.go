// Package httpapi реализует HTTP-адаптер роли api контекста sales-workflow.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивает aggregate root deal/crm-sync, а транзакцию —
// прикладной слой.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"lead-gen/backend/contexts/sales-workflow/internal/app/command"
	"lead-gen/backend/contexts/sales-workflow/internal/app/query"
	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
)

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии
// контекста.
type Dependencies struct {
	CreateDeal      *command.CreateDealHandler
	ChangeDealStage *command.ChangeDealStageHandler
	AttributeResult *command.AttributeResultHandler
	GetDeal         *query.GetDealHandler
	CRMSyncStatus   *query.CRMSyncStatusHandler
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
}

// NewServer собирает маршруты среза из переданных сценариев.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps}
	server.handler = server.routes()
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты среза.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /deals", s.handleCreateDeal)
	mux.HandleFunc("GET /deals/{dealID}", s.handleGetDeal)
	mux.HandleFunc("POST /deals/{dealID}/stage", s.handleChangeStage)
	mux.HandleFunc("POST /deals/{dealID}/attribution", s.handleAttributeResult)
	mux.HandleFunc("GET /deals/{dealID}/crm-sync", s.handleCRMSyncStatus)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	return mux
}

// errorResponse — единая форма ошибки HTTP-слоя.
type errorResponse struct {
	Error string `json:"error"`
}

// decodeJSON читает тело запроса в dst, отвергая неизвестные поля.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("тело запроса отсутствует")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

// WriteJSON отправляет полезную нагрузку с заданным статусом.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteError отправляет ошибку транспорта без разбора по домену.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, errorResponse{Error: message})
}

// writeDomainError переводит ошибку сценария в код ответа.
func writeDomainError(w http.ResponseWriter, err error) {
	status := statusForError(err)
	if status >= http.StatusInternalServerError {
		WriteJSON(w, status, errorResponse{Error: "внутренняя ошибка"})
		return
	}
	WriteJSON(w, status, errorResponse{Error: err.Error()})
}

// statusForError классифицирует ошибку прикладного и доменного слоя. Ошибки не
// найдены приходят как 404, конфликты версий и состояний — как 409, нарушение
// формы запроса — как 400, недоступность внешней CRM — как 503.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrDealNotFound),
		errors.Is(err, ports.ErrCRMSyncNotFound),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, query.ErrTenantMismatch):
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
