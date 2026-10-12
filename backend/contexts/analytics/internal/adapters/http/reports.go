package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// pageReports — имя страницы отчётности в наборе шаблонов.
const pageReports = "templates/reports.html"

// errReportsUnavailable — отчётный обработчик не собран: это недоступность
// зависимости, а не ошибка запроса.
var errReportsUnavailable = errors.New("отчётность не подключена")

// respondReportError переводит ошибку разбора или сборки отчёта в код ответа:
// недоступность обработчика — 503, некорректный фильтр — 400.
func respondReportError(w http.ResponseWriter, err error) {
	if errors.Is(err, errReportsUnavailable) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// handleReports обслуживает GET /reports: HTML-экран отчётности с фильтрами,
// дашбордами, воронкой, качеством базы и управленческим разделом. Показатель
// открытий отсутствует по построению.
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	view, err := s.reportView(r, role)
	if err != nil {
		respondReportError(w, err)
		return
	}
	s.render(w, http.StatusOK, pageReports, components.Page{
		Layout:  components.Layout{Title: consoleMessages().Text(msgReportsTitle)},
		Sidebar: s.consoleNav(r),
		View:    reportPage{ReportView: view, Live: components.Live{ID: "console-live"}, CanManageJobs: s.canManageJobs(r), CSRF: consoleauth.CSRF(r)},
	})
}

// reportPage оборачивает срез отчётности областью живых объявлений: встраивание
// сохраняет доступ к полям среза, а Live добавляет регион aria-live на страницу
// (web-frontend-architecture, срез 7, 7.3). CanManageJobs управляет показом ссылки
// на раздел заданий: недоступное роли не показывается (design.md, Решение 9).
type reportPage struct {
	query.ReportView
	Live          components.Live
	CanManageJobs bool
	CSRF          string
}

// handleReportsJSON обслуживает GET /reports.json: те же агрегаты в JSON. Записи
// пула сюда не попадают — только счётчики и доли.
func (s *Server) handleReportsJSON(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	view, err := s.reportView(r, role)
	if err != nil {
		respondReportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleReportsCSV обслуживает GET /reports/export.csv: выгрузка отчёта в CSV с
// BOM, чтобы Excel читал русские заголовки как UTF-8. Срезы ниже порога
// минимальной группы в файл не попадают.
func (s *Server) handleReportsCSV(w http.ResponseWriter, r *http.Request) {
	view, ok := s.exportView(w, r)
	if !ok {
		return
	}
	body, err := query.RenderCSV(view)
	if err != nil {
		http.Error(w, "сборка выгрузки: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", query.CSVContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+query.ExportFilename("csv", time.Now())+"\"")
	_, _ = w.Write(body)
}

// handleReportsXLSX обслуживает GET /reports/export.xlsx: выгрузка отчёта в XLSX.
// Набор данных тот же, что у CSV, и так же не раскрывает малую группу.
func (s *Server) handleReportsXLSX(w http.ResponseWriter, r *http.Request) {
	view, ok := s.exportView(w, r)
	if !ok {
		return
	}
	body, err := query.RenderXLSX(view)
	if err != nil {
		http.Error(w, "сборка выгрузки: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", query.XLSXContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+query.ExportFilename("xlsx", time.Now())+"\"")
	_, _ = w.Write(body)
}

// exportView собирает срез для выгрузки: проверяет доступ и разбор фильтров.
func (s *Server) exportView(w http.ResponseWriter, r *http.Request) (query.ReportView, bool) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return query.ReportView{}, false
	}
	view, err := s.reportView(r, role)
	if err != nil {
		respondReportError(w, err)
		return query.ReportView{}, false
	}
	return view, true
}

// reportView разбирает фильтры запроса и собирает модель отчёта.
func (s *Server) reportView(r *http.Request, role string) (query.ReportView, error) {
	if s.deps.Reports == nil {
		return query.ReportView{}, errReportsUnavailable
	}
	reportQuery, err := reportQueryFrom(r, role)
	if err != nil {
		return query.ReportView{}, err
	}
	return s.deps.Reports.Handle(r.Context(), reportQuery), nil
}

// reportQueryFrom разбирает параметры фильтра отчёта.
func reportQueryFrom(r *http.Request, role string) (query.ReportQuery, error) {
	params := r.URL.Query()
	from, err := parsePeriodParam(params.Get("from"), false)
	if err != nil {
		return query.ReportQuery{}, err
	}
	to, err := parsePeriodParam(params.Get("to"), true)
	if err != nil {
		return query.ReportQuery{}, err
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return query.ReportQuery{}, errors.New("начало периода позже его конца")
	}
	return query.ReportQuery{
		TenantID:   params.Get("tenant_id"),
		Role:       role,
		Period:     query.ReportPeriod{From: from, To: to},
		Segment:    params.Get("segment"),
		Source:     params.Get("source"),
		CampaignID: params.Get("campaign_id"),
	}, nil
}

// parsePeriodParam разбирает границу периода: дата ГГГГ-ММ-ДД или RFC3339.
// Конец дня расширяется до последней наносекунды, чтобы фильтр включал весь день.
func parsePeriodParam(value string, endOfDay bool) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse("2006-01-02", trimmed); err == nil {
		if endOfDay {
			return parsed.Add(24*time.Hour - time.Nanosecond), nil
		}
		return parsed, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, errors.New("период должен быть датой ГГГГ-ММ-ДД или меткой RFC3339")
	}
	return parsed, nil
}
