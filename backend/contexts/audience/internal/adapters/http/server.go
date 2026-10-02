// Package httpapi реализует HTTP-адаптер роли api контекста audience.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивают агрегаты segment и audience-snapshot, а транзакцию —
// прикладной слой. Обработчики зависят от узких интерфейсов сценариев и не
// ссылаются на хранилище напрямую.
//
// Ресурс GET /audience-snapshots/{snapshotID} отдаёт снимок аудитории в проводной
// форме контракта audience-snapshot: потребитель (outreach-orchestration,
// request-fulfillment) читает его антикоррупционным DTO, поэтому форма полей —
// часть контракта, а не деталь реализации.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"lead-gen/backend/contexts/audience/internal/app/command"
	"lead-gen/backend/contexts/audience/internal/app/query"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"

	// Константы проводной формы контракта audience-snapshot. Значения совпадают с
	// опубликованным контрактом lead-gen/contracts/audience/audiencesnapshot.
	contractAudienceSnapshot = "audience-snapshot"
	schemaVersionAudience    = "1.0.0"
)

// CreateSegmentUseCase — узкий вход сценария создания сегмента.
type CreateSegmentUseCase interface {
	Handle(context.Context, command.CreateSegment) (command.CreateSegmentResult, error)
}

// ScoreLeadUseCase — узкий вход сценария скоринга и сегментации.
type ScoreLeadUseCase interface {
	Handle(context.Context, command.ScoreLead) (command.ScoreLeadResult, error)
}

// FixAudienceSnapshotUseCase — узкий вход сценария фиксации снимка аудитории.
type FixAudienceSnapshotUseCase interface {
	Handle(context.Context, command.FixAudienceSnapshot) (command.FixAudienceSnapshotResult, error)
}

// GetAudienceSnapshotUseCase — узкий вход запроса снимка аудитории.
type GetAudienceSnapshotUseCase interface {
	Handle(context.Context, query.GetAudienceSnapshot) (query.SnapshotView, error)
}

// SegmentMembersUseCase — узкий вход запроса состава сегмента.
type SegmentMembersUseCase interface {
	Handle(context.Context, query.SegmentMembers) (query.SegmentView, error)
}

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии контекста.
type Dependencies struct {
	CreateSegment  CreateSegmentUseCase
	ScoreLead      ScoreLeadUseCase
	FixSnapshot    FixAudienceSnapshotUseCase
	GetSnapshot    GetAudienceSnapshotUseCase
	SegmentMembers SegmentMembersUseCase
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

// ServeHTTP реализует http.Handler, поэтому Server можно передать в http.Server
// или httptest.NewServer без дополнительной обвязки.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты среза. Методы и шаблоны путей разбирает ServeMux
// стандартной библиотеки.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /segments", s.handleCreateSegment)
	mux.HandleFunc("POST /segments/{segmentID}/scores", s.handleScoreLead)
	mux.HandleFunc("GET /segments/{segmentID}/members", s.handleSegmentMembers)
	mux.HandleFunc("POST /audience-snapshots", s.handleFixSnapshot)
	mux.HandleFunc("GET /audience-snapshots/{snapshotID}", s.handleGetSnapshot)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	return mux
}

// errorResponse — единая форма ошибки HTTP-слоя.
type errorResponse struct {
	Error string `json:"error"`
}

// healthResponse — ответ проверки живости.
type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Service: "audience"})
}

// decodeJSON читает тело запроса в dst. Ошибка разбора — ошибка транспорта,
// а не решение о допустимости, поэтому её возвращает вызывающий.
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
	// Ошибку кодирования сообщить в ответ уже нельзя: заголовки отправлены.
	_ = json.NewEncoder(w).Encode(payload)
}

// writeErrorMessage отправляет ошибку транспорта без разбора по домену.
func writeErrorMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// writeDomainError переводит ошибку сценария в код ответа. Доменную ошибку
// адаптер только классифицирует; он не решает, допустима ли операция.
func writeDomainError(w http.ResponseWriter, err error) {
	status := statusForError(err)
	if status >= http.StatusInternalServerError {
		writeJSON(w, status, errorResponse{Error: "внутренняя ошибка"})
		return
	}
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

// statusForError классифицирует ошибку прикладного и доменного слоя. Отсутствие
// агрегата — 404 (чужой тенант неотличим от отсутствия), конфликты версий и
// тенанта — 409, нарушение формы запроса — 400.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrSegmentNotFound), errors.Is(err, ports.ErrSnapshotNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrVersionConflict),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, domain.ErrTenantMismatch):
		return http.StatusConflict
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingEventID),
		errors.Is(err, domain.ErrEmptySegmentName),
		errors.Is(err, domain.ErrInvalidScore),
		errors.Is(err, domain.ErrUnknownModelVersion),
		errors.Is(err, domain.ErrUnknownSignal),
		errors.Is(err, domain.ErrDuplicateSignal),
		errors.Is(err, domain.ErrInvalidSignal),
		errors.Is(err, domain.ErrEmptyCriteria),
		errors.Is(err, domain.ErrInvalidCriteria),
		errors.Is(err, domain.ErrRequiredSignalsWithoutMatch),
		errors.Is(err, domain.ErrNoSignals),
		errors.Is(err, domain.ErrTenantImmutable):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
