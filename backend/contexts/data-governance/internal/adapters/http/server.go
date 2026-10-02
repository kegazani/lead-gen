// Package httpapi реализует HTTP-адаптер роли api контекста data-governance.
//
// Адаптер только разбирает запрос, проверяет право актора на действие и вызывает
// прикладной сценарий, переводя доменную ошибку в код ответа. Решений о
// допустимости он не принимает: инварианты обеспечивает aggregate root
// processing-basis, а чистые политики — пакет domain. Обработчики зависят от
// сценариев и не ссылаются на хранилище напрямую.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"lead-gen/backend/contexts/data-governance/internal/adapters/access"
	"lead-gen/backend/contexts/data-governance/internal/app/command"
	"lead-gen/backend/contexts/data-governance/internal/app/query"
	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
)

// errAccessDenied — отказ в доступе к операции. Чужой тенант и отсутствие права
// дают одинаковый ответ: существование данных не раскрывается.
var errAccessDenied = errors.New("доступ запрещён")

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии
// контекста и решение о доступе.
type Dependencies struct {
	CreateBasis     *command.CreateBasisHandler
	ChangeAuthority *command.ChangeAuthorityHandler
	RevokeBasis     *command.RevokeBasisHandler
	SuppressContact *command.SuppressContactHandler
	LiftSuppression *command.LiftSuppressionHandler
	GetBasis        *query.GetBasisHandler
	CheckZone       *query.CheckZoneHandler
	CheckSuppress   *query.CheckSuppressionHandler

	// Access — решение о доступе актора к тенанту. nil означает, что источник
	// решения не подключён: изменяющие операции честно отвечают 503.
	Access ports.AccessDecision
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
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /bases", s.handleCreateBasis)
	mux.HandleFunc("GET /bases/{basisID}", s.handleGetBasis)
	mux.HandleFunc("POST /bases/{basisID}/authority", s.handleChangeAuthority)
	mux.HandleFunc("POST /bases/{basisID}/revocation", s.handleRevokeBasis)
	mux.HandleFunc("POST /bases/{basisID}/suppressions", s.handleSuppressContact)
	mux.HandleFunc("POST /bases/{basisID}/suppressions/{suppressionID}/lift", s.handleLiftSuppression)
	mux.HandleFunc("GET /zone-policy", s.handleCheckZone)
	mux.HandleFunc("GET /suppressions/check", s.handleCheckSuppression)
	return mux
}

// errorResponse — единая форма ошибки HTTP-слоя.
type errorResponse struct {
	Error string `json:"error"`
}

// authorize проверяет право актора действовать в тенанте. Отсутствие источника
// решения — 503: адаптер не делает вид, что доступ разрешён.
func (s *Server) authorize(r *http.Request, tenant domain.TenantID, actor domain.ActorID) error {
	if s.deps.Access == nil {
		return fmt.Errorf("%w: источник решения о доступе не подключён", ports.ErrAccessUnavailable)
	}
	allowed, err := s.deps.Access.CanAct(r.Context(), actor, tenant)
	if err != nil {
		return err
	}
	if !allowed {
		return errAccessDenied
	}
	return nil
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

// writeErrorMessage отправляет ошибку транспорта без разбора по домену.
func writeErrorMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
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
// найдены приходят как 404, конфликты версий и состояний — как 409, нарушение
// формы запроса — как 400, отсутствие права — как 403, недоступный источник
// решения — как 503.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrBasisNotFound), errors.Is(err, domain.ErrSuppressionNotFound):
		return http.StatusNotFound
	case errors.Is(err, errAccessDenied):
		return http.StatusForbidden
	case errors.Is(err, ports.ErrAccessUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, access.ErrMissingSubject):
		return http.StatusBadRequest
	case errors.Is(err, ports.ErrVersionConflict),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, domain.ErrBasisRevoked),
		errors.Is(err, domain.ErrAlreadySuppressed),
		errors.Is(err, domain.ErrSuppressionNotActive):
		return http.StatusConflict
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrUnknownCategory),
		errors.Is(err, domain.ErrEmptyPurpose),
		errors.Is(err, domain.ErrInvalidRetention),
		errors.Is(err, domain.ErrInvalidValidityWindow),
		errors.Is(err, domain.ErrReasonRequired):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
