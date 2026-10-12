// Package httpapi реализует HTTP-адаптер публичной роли ingest контекста
// engagement-observation: приём сырого сигнала взаимодействия от провайдера или
// письма, редирект по трекинг-ссылке (read-signal) и чтение зафиксированного
// события.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости сигнала он не принимает:
// квалификацию, дедупликацию и запрет пикселей обеспечивает домен, а транзакцию —
// прикладной слой. Пиксельных эндпоинтов здесь нет по построению.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/engagement-observation/internal/app/command"
	"lead-gen/backend/contexts/engagement-observation/internal/app/query"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	"lead-gen/backend/platform/readiness"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
	contentTypeHTML = "text/html; charset=utf-8"
)

// Dependencies — прикладные сценарии, которыми пользуется HTTP-адаптер.
type Dependencies struct {
	Readiness func(context.Context) readiness.Snapshot
	// IngestEvent — приём и квалификация сырого сигнала.
	IngestEvent *command.IngestEventHandler
	// RecordTransition — учёт перехода по трекинг-ссылке (read-signal).
	RecordTransition *command.RecordTransitionHandler
	// GetEngagement — чтение зафиксированного события.
	GetEngagement *query.GetEngagementHandler
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте NewServer.
type Server struct {
	deps         Dependencies
	handler      http.Handler
	notFoundPage *template.Template
}

// NewServer собирает маршруты среза. Разметка страницы «ссылка не найдена»
// разбирается сразу: ошибка шаблона видна при сборке, а не в момент перехода.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps}
	server.notFoundPage = template.Must(template.New("not-found").Parse(notFoundTemplate))
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
	mux.HandleFunc("POST /events", s.handleIngestEvent)
	mux.HandleFunc("GET /r/{ref}", s.handleResolve)
	mux.HandleFunc("GET /engagements/{id}", s.handleGetEngagement)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	return mux
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.deps.Readiness == nil {
		writeJSON(w, http.StatusServiceUnavailable, readiness.Snapshot{Status: readiness.StatusUnknown, Ready: false})
		return
	}
	report := s.deps.Readiness(r.Context())
	status := http.StatusOK
	if !report.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, report)
}

// healthResponse — форма ответа проверки живости.
type healthResponse struct {
	Status string `json:"status"`
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// ingestRequest — тело приёма сырого сигнала. Поля — сырые подписи источника;
// пиксельных видов здесь нет, а попытка их прислать отвергается доменом.
type ingestRequest struct {
	SignalID      string `json:"signal_id"`
	Source        string `json:"source"`
	TenantID      string `json:"tenant_id"`
	CompanyID     string `json:"company_id"`
	Kind          string `json:"kind"`
	Outcome       string `json:"outcome"`
	LinkRef       string `json:"link_ref"`
	Target        string `json:"target"`
	OccurredAt    string `json:"occurred_at"`
	Confidence    string `json:"confidence"`
	CorrelationID string `json:"correlation_id"`
	CausationID   string `json:"causation_id"`
}

// ingestResponse — ответ приёма сигнала.
type ingestResponse struct {
	EngagementEventID string `json:"engagement_event_id,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Duplicate         bool   `json:"duplicate"`
}

// handleIngestEvent обслуживает POST /events: принимает сырой сигнал, дедуплицирует
// и квалифицирует его. Повтор сигнала отвечает 200 с duplicate=true; новое событие
// — 202 Accepted.
func (s *Server) handleIngestEvent(w http.ResponseWriter, r *http.Request) {
	if s.deps.IngestEvent == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "приём сигналов недоступен")
		return
	}
	var req ingestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	occurred, err := parseOptionalTime(req.OccurredAt)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.deps.IngestEvent.Handle(r.Context(), command.IngestEvent{
		SignalID:      req.SignalID,
		Source:        req.Source,
		TenantID:      req.TenantID,
		CompanyID:     req.CompanyID,
		Kind:          req.Kind,
		Outcome:       req.Outcome,
		LinkRef:       req.LinkRef,
		Target:        req.Target,
		OccurredAt:    occurred,
		Confidence:    req.Confidence,
		CorrelationID: req.CorrelationID,
		CausationID:   req.CausationID,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := http.StatusAccepted
	if result.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, ingestResponse{
		EngagementEventID: result.EngagementEventID,
		Kind:              result.Kind,
		Duplicate:         result.Duplicate,
	})
}

// handleResolve обслуживает GET /r/{ref}: разрешает трекинг-ссылку, учитывает
// состоявшийся переход и уводит посетителя на адрес назначения. Неизвестная
// ссылка отдаёт страницу «не найдено», а не выдуманный адрес.
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	if s.deps.RecordTransition == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "трекинг-переходы недоступны")
		return
	}
	ref := r.PathValue("ref")
	result, err := s.deps.RecordTransition.Handle(r.Context(), command.RecordTransition{
		Ref:    ref,
		Source: "redirect",
	})
	if err != nil {
		if errors.Is(err, ports.ErrLinkNotFound) {
			s.writeNotFound(w)
			return
		}
		writeDomainError(w, err)
		return
	}
	http.Redirect(w, r, result.Target, http.StatusFound)
}

// handleGetEngagement обслуживает GET /engagements/{id}: отдаёт зафиксированное
// событие. Тенант обязателен: чтение события другого тенанта завершается «не
// найдено», и существование чужого события этим не раскрывается.
func (s *Server) handleGetEngagement(w http.ResponseWriter, r *http.Request) {
	if s.deps.GetEngagement == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "чтение событий недоступно")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if tenant == "" {
		writeErrorMessage(w, http.StatusBadRequest, "параметр tenant_id обязателен")
		return
	}
	view, err := s.deps.GetEngagement.Handle(r.Context(), query.GetEngagement{
		ID:       r.PathValue("id"),
		TenantID: tenant,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// writeNotFound отдаёт HTML-страницу «ссылка не найдена».
func (s *Server) writeNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(http.StatusNotFound)
	if s.notFoundPage == nil {
		_, _ = w.Write([]byte("трекинг-ссылка не найдена"))
		return
	}
	if err := s.notFoundPage.Execute(w, nil); err != nil {
		_, _ = w.Write([]byte("трекинг-ссылка не найдена"))
	}
}

// notFoundTemplate — разметка страницы «ссылка не найдена». Отдельного
// трекингового пикселя здесь нет: страница только сообщает об исходе.
const notFoundTemplate = `<!doctype html>
<html lang="ru">
<head><meta charset="utf-8"><title>Ссылка не найдена</title></head>
<body><h1>Ссылка не найдена</h1><p>Трекинг-ссылка неизвестна или устарела.</p></body>
</html>
`

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

// parseOptionalTime разбирает момент сигнала: пустая строка — нулевое время
// (часы сборки подставят момент приёма), иначе — RFC3339.
func parseOptionalTime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, errors.New("поле occurred_at должно быть в формате RFC3339")
	}
	return parsed, nil
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

// writeDomainError переводит ошибку сценария в код ответа. Пиксельный сигнал —
// осознанный отказ политики, поэтому отвечает 422, а не «некорректный запрос».
func writeDomainError(w http.ResponseWriter, err error) {
	status := statusForError(err)
	if status >= http.StatusInternalServerError {
		writeJSON(w, status, errorResponse{Error: "внутренняя ошибка"})
		return
	}
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

// statusForError классифицирует ошибку прикладного и доменного слоя.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrEventNotFound), errors.Is(err, ports.ErrLinkNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrPixelBasedKind):
		return http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrMissingEventID),
		errors.Is(err, domain.ErrUnknownKind),
		errors.Is(err, domain.ErrUnknownConfidence),
		errors.Is(err, domain.ErrUnknownOutcome),
		errors.Is(err, domain.ErrMissingLinkRef),
		errors.Is(err, domain.ErrMissingTarget):
		return http.StatusBadRequest
	case errors.Is(err, ports.ErrVersionConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// Проверка, что сервер реализует http.Handler.
var _ http.Handler = (*Server)(nil)
