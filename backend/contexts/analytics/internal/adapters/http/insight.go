package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/frontend/components"
)

// pageInsights — имя страницы анализа качества поиска в наборе шаблонов.
const pageInsights = "templates/insights.html"

// errInsightsUnavailable — обработчик анализа не собран: недоступность
// зависимости, а не ошибка запроса.
var errInsightsUnavailable = errors.New("анализ качества поиска не подключён")

// handleInsights обслуживает GET /insights: HTML-экран анализа качества поиска —
// распределение вердиктов и причин «не подходит» по разрезам и рекомендации с
// основанием. Персональные данные и красная зона сюда не попадают.
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceInsights) {
		return
	}
	view, err := s.insightView(r, role)
	if err != nil {
		respondInsightError(w, err)
		return
	}
	s.render(w, http.StatusOK, pageInsights, components.Page{
		Layout:  components.Layout{Title: consoleMessages().Text(msgInsightsTitle)},
		Sidebar: s.consoleNav(r),
		View:    insightPage{InsightView: view, Live: components.Live{ID: "console-live"}},
	})
}

// insightPage оборачивает срез анализа областью живых объявлений.
type insightPage struct {
	query.InsightView
	Live components.Live
}

// handleInsightsJSON обслуживает GET /insights.json: те же агрегаты в JSON.
func (s *Server) handleInsightsJSON(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceInsights) {
		return
	}
	view, err := s.insightView(r, role)
	if err != nil {
		respondInsightError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleInsightApplicationsJSON обслуживает GET /insights/applications.json:
// сравнение показателей «до/после» по применённым рекомендациям.
func (s *Server) handleInsightApplicationsJSON(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceInsights) {
		return
	}
	if s.deps.Applications == nil {
		http.Error(w, "сравнение применений не подключено", http.StatusServiceUnavailable)
		return
	}
	view := s.deps.Applications.Handle(r.Context(), query.ApplicationQuery{
		TenantID:  r.URL.Query().Get("tenant_id"),
		ScopeKind: r.URL.Query().Get("scope_kind"),
		ScopeKey:  r.URL.Query().Get("scope_key"),
	})
	writeJSON(w, http.StatusOK, view)
}

// applyRecommendationDTO — тело запроса применения рекомендации. Изменение
// контура поиска структурно: вид, критерий, цели обхода и лимит, а не проза.
type applyRecommendationDTO struct {
	TenantID  string          `json:"tenant_id"`
	ScopeKind string          `json:"scope_kind"`
	ScopeKey  string          `json:"scope_key"`
	Reason    string          `json:"reason"`
	Basis     string          `json:"basis"`
	Change    searchChangeDTO `json:"change"`
}

// searchChangeDTO — структурное изменение контура поиска в теле запроса.
type searchChangeDTO struct {
	Kind     string   `json:"kind"`
	Criteria string   `json:"criteria"`
	Targets  []string `json:"targets"`
	Limit    int      `json:"limit"`
}

// handleApplyRecommendation обслуживает POST /insights/recommendations/apply:
// применяет рекомендацию как изменение в контуре поиска и фиксирует факт
// применения. Недоступность контура поиска — честный отказ, а не успех без эффекта.
func (s *Server) handleApplyRecommendation(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceInsights) {
		return
	}
	if s.deps.ApplyRecommendation == nil {
		http.Error(w, "применение рекомендации не подключено", http.StatusServiceUnavailable)
		return
	}
	var dto applyRecommendationDTO
	if err := decodeJSON(w, r, &dto); err != nil {
		http.Error(w, "разбор применения: "+err.Error(), http.StatusBadRequest)
		return
	}
	application, err := s.deps.ApplyRecommendation.Handle(r.Context(), command.ApplyRecommendation{
		TenantID:  dto.TenantID,
		ScopeKind: dto.ScopeKind,
		ScopeKey:  dto.ScopeKey,
		Reason:    dto.Reason,
		Basis:     dto.Basis,
		Change: ports.SearchChange{
			Kind:     dto.Change.Kind,
			Criteria: dto.Change.Criteria,
			Targets:  dto.Change.Targets,
			Limit:    dto.Change.Limit,
		},
	})
	if err != nil {
		http.Error(w, "применение рекомендации: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":    "applied",
		"reference": application.Reference,
	})
}

// insightView разбирает фильтры запроса и собирает срез анализа.
func (s *Server) insightView(r *http.Request, role string) (query.InsightView, error) {
	if s.deps.Insights == nil {
		return query.InsightView{}, errInsightsUnavailable
	}
	insightQuery, err := insightQueryFrom(r, role)
	if err != nil {
		return query.InsightView{}, err
	}
	return s.deps.Insights.Handle(r.Context(), insightQuery), nil
}

// insightQueryFrom разбирает параметры фильтра анализа.
func insightQueryFrom(r *http.Request, role string) (query.InsightQuery, error) {
	params := r.URL.Query()
	from, err := parsePeriodParam(params.Get("from"), false)
	if err != nil {
		return query.InsightQuery{}, err
	}
	to, err := parsePeriodParam(params.Get("to"), true)
	if err != nil {
		return query.InsightQuery{}, err
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return query.InsightQuery{}, errors.New("начало периода позже его конца")
	}
	filters := query.InsightFilters{
		JobID:    strings.TrimSpace(params.Get("job_id")),
		Source:   strings.TrimSpace(params.Get("source")),
		Industry: strings.TrimSpace(params.Get("industry")),
	}
	if !from.IsZero() {
		filters.From = from.UTC().Format("2006-01-02")
	}
	if !to.IsZero() {
		filters.To = to.UTC().Format("2006-01-02")
	}
	return query.InsightQuery{
		TenantID: params.Get("tenant_id"),
		Role:     role,
		Filter: readmodel.InsightFilter{
			JobID:    filters.JobID,
			Source:   filters.Source,
			Industry: filters.Industry,
			From:     from,
			To:       to,
		},
		Filters: filters,
	}, nil
}

// respondInsightError переводит ошибку сборки анализа в код ответа: недоступность
// обработчика — 503, некорректный фильтр — 400.
func respondInsightError(w http.ResponseWriter, err error) {
	if errors.Is(err, errInsightsUnavailable) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}
