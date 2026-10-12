// Package httpapi — HTTP-поверхность роли api контекста integration-platform.
//
// Поверхность отдаёт владельческие данные, которые уже нужны потребителям и
// объявлены каталогом: состояние объявленных адаптеров (контракт adapter-status,
// связь integrations-status-for-governance) и реестр зарегистрированных
// адаптеров. Внешних ключей поверхность не требует: состояние адаптеров — это
// факт конфигурации точки сборки, а реестр — чтение хранилища регистраций.
//
// Поверхность не выдумывает интеграцию: если источник состояния не подключён,
// запрос честно отвечает 503, а не пустым списком. Свободного текста в отдаваемых
// данных нет — только значения закрытых словарей владельца.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/app"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/integration-platform/adapterstatus"
)

// Dependencies — зависимости HTTP-поверхности роли api. Statuses обязателен:
// без него запрос состояния честно недоступен. Registrations необязателен: без
// него реестр отвечает недоступностью.
type Dependencies struct {
	// Statuses — источник состояния объявленных адаптеров (владелец данных).
	Statuses ports.AdapterStatusReader
	// Registrations — use case чтения реестра зарегистрированных адаптеров.
	Registrations *app.ListAdaptersHandler
	// Readiness — проверка API-зависимостей владельца. nil всегда означает
	// unknown/not-ready, а не успешную пустую проверку.
	Readiness func(context.Context) readiness.Snapshot
}

// Server — HTTP-сервер роли api. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
}

// NewServer собирает маршруты роли api.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps}
	server.handler = server.routes()
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты роли api.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReadiness)
	mux.HandleFunc("GET /adapter-status", s.handleAdapterStatus)
	mux.HandleFunc("GET /adapters", s.handleAdapters)
	return mux
}

// handleHealth отвечает на проверку живости.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

type readinessResponse struct {
	Capability string                   `json:"capability"`
	Status     readiness.Status         `json:"status"`
	Ready      bool                     `json:"ready"`
	Checks     []readinessCheckResponse `json:"checks"`
}

type readinessCheckResponse struct {
	ID         string               `json:"id"`
	Status     readiness.Status     `json:"status"`
	Reason     readiness.ReasonCode `json:"reason"`
	ObservedAt time.Time            `json:"observed_at"`
	Required   bool                 `json:"required"`
}

// handleReadiness сообщает только зависимости, обязательные для роли API.
// Состояние внешних провайдеров отдельно доступно в /adapter-status.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	snapshot := readiness.Snapshot{Capability: "integration-platform-api", Status: readiness.StatusUnknown, Ready: false}
	if s.deps.Readiness != nil {
		snapshot = s.deps.Readiness(r.Context())
	}
	checks := make([]readinessCheckResponse, 0, len(snapshot.Checks))
	for _, check := range snapshot.Checks {
		checks = append(checks, readinessCheckResponse{
			ID: check.ID, Status: check.Status, Reason: check.Reason,
			ObservedAt: check.ObservedAt, Required: check.Required,
		})
	}
	status := http.StatusOK
	if !snapshot.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, readinessResponse{
		Capability: snapshot.Capability, Status: snapshot.Status,
		Ready: snapshot.Ready, Checks: checks,
	})
}

// handleAdapterStatus отдаёт состояние объявленных адаптеров в форме контракта
// adapter-status. Если источник не подключён, отвечает 503: unavailable, а не
// пустым списком.
func (s *Server) handleAdapterStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.Statuses == nil {
		writeError(w, http.StatusServiceUnavailable, "источник состояния адаптеров не подключён")
		return
	}
	statuses, err := s.deps.Statuses.AdapterStatuses(r.Context(), r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "источник состояния адаптеров недоступен")
		return
	}
	if statuses == nil {
		statuses = []adapterstatus.Status{}
	}
	writeJSON(w, http.StatusOK, adapterstatus.Response{
		Contract:      adapterstatus.ContractName,
		SchemaVersion: adapterstatus.SchemaVersion,
		Adapters:      statuses,
	})
}

// adaptersResponse — тело ответа реестра адаптеров.
type adaptersResponse struct {
	Adapters []adapterView `json:"adapters"`
}

// adapterView — элемент реестра: имя адаптера, версия manifest и возможности.
type adapterView struct {
	Adapter      string   `json:"adapter"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// handleAdapters отдаёт реестр зарегистрированных адаптеров. Пустой реестр —
// пустой список без ошибки; недоступность хранилища — 503.
func (s *Server) handleAdapters(w http.ResponseWriter, r *http.Request) {
	if s.deps.Registrations == nil {
		writeError(w, http.StatusServiceUnavailable, "реестр адаптеров не собран")
		return
	}
	views, err := s.deps.Registrations.Handle(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "реестр адаптеров недоступен")
		return
	}
	out := make([]adapterView, 0, len(views))
	for _, view := range views {
		capabilities := view.Capabilities
		if capabilities == nil {
			capabilities = []string{}
		}
		out = append(out, adapterView{
			Adapter:      view.Adapter,
			Version:      view.Version,
			Capabilities: capabilities,
		})
	}
	writeJSON(w, http.StatusOK, adaptersResponse{Adapters: out})
}

// errorBody — тело ответа об ошибке. Текст нормализован на стороне владельца:
// технических деталей и секретов здесь нет.
type errorBody struct {
	Error string `json:"error"`
}

// writeJSON отдаёт тело как JSON с кодом ответа.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError отдаёт нормализованную ошибку как JSON.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message})
}
