// Package httpapi реализует HTTP-адаптер роли api контекста publishing.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивает aggregate root publication, а транзакцию — прикладной
// слой. Публикация вовне идёт через порт площадки, а не из HTTP-обработчика
// напрямую.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"lead-gen/backend/contexts/publishing/internal/app/command"
	"lead-gen/backend/contexts/publishing/internal/app/query"
	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
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
	RequestPublication *command.RequestPublicationHandler
	Approve            *command.ApproveHandler
	Reject             *command.RejectHandler
	Publish            *command.PublishHandler
	GetPublication     *query.GetPublicationHandler
	PublicationQueue   *query.PublicationQueueHandler
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
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
	// Корень сервиса отдаёт дескриптор, а не 404: HTML-экрана у API нет.
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("POST /publications", s.handleRequestPublication)
	mux.HandleFunc("GET /publications", s.handlePublicationQueue)
	mux.HandleFunc("GET /publications/{publicationID}", s.handleGetPublication)
	mux.HandleFunc("POST /publications/{publicationID}/approval", s.handleApprove)
	mux.HandleFunc("POST /publications/{publicationID}/rejection", s.handleReject)
	mux.HandleFunc("POST /publications/{publicationID}/publication", s.handlePublish)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	return mux
}

// handleRoot обслуживает GET /{$}: корень сервиса отдаёт краткий дескриптор, а
// не 404 — HTML-экрана у API нет, и корень честно отвечает, что за сервис здесь.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "publishing"})
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
		return fmt.Errorf("разбор JSON: %w", err)
	}
	return nil
}

// writeJSON отправляет полезную нагрузку с заданным статусом.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// writeDomainError переводит ошибку сценария в код ответа.
func writeDomainError(w http.ResponseWriter, err error) {
	status := statusForError(err)
	if status >= http.StatusInternalServerError {
		writeJSON(w, status, errorResponse{Error: "внутренняя ошибка"})
		return
	}
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

// statusForError классифицирует ошибку прикладного и доменного слоя. Ошибки не
// найдены приходят как 404, конфликты состояний — как 409, нарушение формы
// запроса — как 400, недоступность канала — как 503, сбой чтения — как 502.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrPublicationNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrVersionConflict),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, command.ErrDuplicateTarget),
		errors.Is(err, domain.ErrInvalidTransition),
		errors.Is(err, domain.ErrTerminalState):
		return http.StatusConflict
	case errors.Is(err, command.ErrSourceNotFound):
		return http.StatusUnprocessableEntity
	case errors.Is(err, ports.ErrChannelNotReady),
		errors.Is(err, ports.ErrPlatformUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrUnknownPlatform),
		errors.Is(err, domain.ErrEmptyAccount),
		errors.Is(err, domain.ErrEmptyMaterial),
		errors.Is(err, domain.ErrDigestRequired),
		errors.Is(err, domain.ErrApprovalRequired),
		errors.Is(err, domain.ErrReasonRequired),
		errors.Is(err, domain.ErrUnknownStatus),
		errors.Is(err, command.ErrNoTargets):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
