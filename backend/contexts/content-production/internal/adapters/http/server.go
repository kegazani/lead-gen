// Package httpapi реализует HTTP-адаптер роли api контекста content-production.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивают агрегаты content-package и render-job, а транзакцию —
// прикладной слой. Обработчики зависят от сценариев и не ссылаются на хранилище
// напрямую.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/app/linguistics"
	"lead-gen/backend/contexts/content-production/internal/app/query"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
	"lead-gen/backend/platform/readiness"
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
	Readiness               func(context.Context) readiness.Snapshot
	CreateContentPackage    *command.CreateContentPackageHandler
	PublishContentVersion   *command.PublishContentVersionHandler
	RequestRender           *command.RequestRenderHandler
	ProduceContent          *command.ProduceContentHandler
	RestoreContentVersion   *command.RestoreContentVersionHandler
	GetContent              *query.GetContentHandler
	GetRenderJob            *query.GetRenderJobHandler
	GetLandingPage          *query.GetLandingPageHandler
	ProposeLandingStructure *query.ProposeLandingStructureHandler
	GenerateLandingTexts    *command.GenerateLandingTextsHandler
	CollectSite             *command.CollectSiteHandler
	ParseDocuments          *command.ParseDocumentsHandler
	CheckLandingTexts       *command.CheckLandingTextsHandler
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

	mux.HandleFunc("POST /content/packages", s.handleCreatePackage)
	mux.HandleFunc("POST /content/packages/{assetID}/versions", s.handlePublishVersion)
	mux.HandleFunc("POST /content/packages/{assetID}/versions/{versionID}/restore", s.handleRestoreVersion)
	mux.HandleFunc("GET /content/packages/{assetID}", s.handleGetContent)
	mux.HandleFunc("POST /content-version", s.handleContentVersion)
	mux.HandleFunc("GET /content/packages/{assetID}/landing-page", s.handleGetLandingPage)
	mux.HandleFunc("POST /content/packages/{assetID}/render-jobs", s.handleRequestRender)
	mux.HandleFunc("POST /content/produce", s.handleProduce)
	mux.HandleFunc("POST /content/landing-structure", s.handleProposeLandingStructure)
	mux.HandleFunc("POST /content/landing-texts", s.handleGenerateLandingTexts)
	mux.HandleFunc("POST /content/landing-collect", s.handleCollectSite)
	mux.HandleFunc("POST /content/landing-documents", s.handleParseDocuments)
	mux.HandleFunc("POST /content/landing-check", s.handleCheckLandingTexts)
	mux.HandleFunc("GET /content/render-jobs/{renderJobID}", s.handleGetRenderJob)
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
// формы запроса — как 400.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrContentPackageNotFound),
		errors.Is(err, ports.ErrRenderJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrVersionConflict),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, domain.ErrTerminalRenderState),
		errors.Is(err, domain.ErrInvalidRenderTransition):
		return http.StatusConflict
	case errors.Is(err, ports.ErrRenderUnavailable),
		errors.Is(err, ports.ErrSourcesUnavailable),
		errors.Is(err, linguistics.ErrUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ports.ErrRenderRejected):
		return http.StatusBadGateway
	case errors.Is(err, domain.ErrRestoreSelf):
		return http.StatusConflict
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrInvalidBrandKit),
		errors.Is(err, domain.ErrCompanyNameRequired),
		errors.Is(err, domain.ErrInvalidContactKind),
		errors.Is(err, domain.ErrInvalidContactValue),
		errors.Is(err, domain.ErrInvalidLeadForm),
		errors.Is(err, domain.ErrFormActionRequired),
		errors.Is(err, domain.ErrMaterialsInsufficient),
		errors.Is(err, domain.ErrUnknownBlockRole),
		errors.Is(err, domain.ErrStructureNotApproved),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrMissingEventID),
		errors.Is(err, domain.ErrEmptyContent),
		errors.Is(err, domain.ErrInvalidLocale),
		errors.Is(err, domain.ErrUnknownRenderFormat),
		errors.Is(err, domain.ErrUnknownRenderState),
		errors.Is(err, domain.ErrTemplateRequired),
		errors.Is(err, domain.ErrVersionRequired),
		errors.Is(err, domain.ErrReasonRequired),
		errors.Is(err, domain.ErrArtifactRequired),
		errors.Is(err, domain.ErrArtifactFormatMismatch),
		errors.Is(err, domain.ErrVersionNotFound),
		errors.Is(err, command.ErrVersionRequired),
		errors.Is(err, command.ErrPackageRequired),
		errors.Is(err, command.ErrCollectionInput),
		errors.Is(err, command.ErrDocumentInput),
		errors.Is(err, linguistics.ErrInvalidRequest),
		errors.Is(err, linguistics.ErrUnsourcedClaims):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
