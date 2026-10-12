package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/frontend/components"
)

// Имена новых страниц набора шаблонов консоли аналитики.
const (
	// pageExportProgress — страница хода длительной выгрузки отчёта.
	pageExportProgress = "templates/export_progress.html"
	// pageJobs — админ-экран заданий, исчерпавших попытки (DLQ).
	pageJobs = "templates/jobs.html"
)

// exportProgressPath собирает путь страницы прогресса задания. Путь относительный:
// сервис не обязан знать свой внешний адрес.
func exportProgressPath(id string) string { return "/reports/exports/" + id + "/progress" }

// handleReportExportEnqueueHTML обслуживает POST /reports/exports/html: ставит
// длительную выгрузку в очередь и возвращает POST/redirect/GET на страницу
// прогресса, а не блокируется на сборке файла. Тот же use case, что у JSON-пути.
func (s *Server) handleReportExportEnqueueHTML(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	if s.deps.Exports == nil {
		http.Error(w, "выгрузка отчёта не подключена", http.StatusServiceUnavailable)
		return
	}
	format, err := parseExportFormat(r.URL.Query().Get("format"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reportQuery, err := reportQueryFrom(r, role)
	if err != nil {
		respondReportError(w, err)
		return
	}
	record, err := s.deps.Exports.Enqueue(r.Context(), ports.ReportExportRequest{
		TenantID:   reportQuery.TenantID,
		Role:       reportQuery.Role,
		From:       reportQuery.Period.From,
		To:         reportQuery.Period.To,
		Segment:    reportQuery.Segment,
		Source:     reportQuery.Source,
		CampaignID: reportQuery.CampaignID,
		Format:     format,
	})
	if err != nil {
		http.Error(w, "постановка выгрузки: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, exportProgressPath(record.ID), http.StatusSeeOther)
}

// handleReportExportProgress обслуживает GET /reports/exports/{id}/progress: ход
// задания. Запрос HTMX получает только фрагмент, обычный — полную страницу (design
// decision 4: один обработчик, полная страница или фрагмент).
func (s *Server) handleReportExportProgress(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	if s.deps.ExportJobs == nil {
		http.Error(w, "выгрузка отчёта не подключена", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	record, err := s.deps.ExportJobs.Status(r.Context(), id)
	if errors.Is(err, ports.ErrReportExportNotFound) {
		http.Error(w, "задание выгрузки не найдено", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "чтение задания выгрузки", http.StatusInternalServerError)
		return
	}

	view := exportProgressViewOf(record, s.canManageJobs(r), navContext(r))
	if isHTMX(r) {
		s.renderFragment(w, pageExportProgress, "export-progress", view)
		return
	}
	s.render(w, http.StatusOK, pageExportProgress, components.Page{
		Layout:  components.Layout{Title: consoleMessages().Text(msgExportProgressTitle)},
		Sidebar: s.consoleNav(r),
		View:    view,
	})
}

// handleReportExportRetry обслуживает POST /reports/exports/{id}/retry: возвращает
// упавшее задание в очередь (примитив jobs.RetryJob) и возвращается на страницу
// прогресса (PRG). Повтор — явное действие оператора, а не автоматическая политика.
func (s *Server) handleReportExportRetry(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	if s.deps.Jobs == nil {
		http.Error(w, "повтор заданий не подключён", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := s.deps.Jobs.Retry(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, jobs.ErrNotFound):
			http.Error(w, "задание выгрузки не найдено", http.StatusNotFound)
		case errors.Is(err, jobs.ErrTransitionRejected):
			http.Error(w, "задание не в состоянии отказа: повтор не требуется", http.StatusConflict)
		case errors.Is(err, jobs.ErrUnsupported):
			http.Error(w, "повтор заданий не поддерживается хранилищем", http.StatusServiceUnavailable)
		default:
			http.Error(w, "повтор задания выгрузки", http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, exportProgressPath(id), http.StatusSeeOther)
}

// handleReportJobs обслуживает GET /reports/jobs: админ-экран заданий, исчерпавших
// попытки (DLQ). Задание показывается с нормализованной причиной и действием
// повтора; технические детали сюда не попадают (их положил раннер как формулировку
// для оператора, а не текст ошибки).
func (s *Server) handleReportJobs(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceJobs) {
		return
	}
	if s.deps.Jobs == nil {
		http.Error(w, "видимость заданий не подключена", http.StatusServiceUnavailable)
		return
	}
	list, err := s.deps.Jobs.DLQ(r.Context(), ports.ExportJobMaxAttempts, ports.ExportJobDLQLimit)
	if err != nil {
		http.Error(w, "список заданий недоступен", http.StatusServiceUnavailable)
		return
	}

	messages := consoleMessages()
	rows := make([]jobRow, 0, len(list))
	for _, job := range list {
		reason := strings.TrimSpace(job.LastError)
		if reason == "" {
			reason = messages.Text(msgJobsNoReason)
		}
		rows = append(rows, jobRow{
			ID:           job.ID,
			Attempts:     job.Attempts,
			Reason:       reason,
			UpdatedLabel: components.DefaultFormatter().DateTime(job.UpdatedAt.UTC()),
		})
	}

	s.render(w, http.StatusOK, pageJobs, components.Page{
		Layout:  components.Layout{Title: messages.Text(msgJobsTitle)},
		Sidebar: s.consoleNav(r),
		View: jobsView{
			Rows:    rows,
			Empty:   len(rows) == 0,
			Retried: strings.TrimSpace(r.URL.Query().Get("retried")),
			Live:    components.Live{ID: "console-live"},
		},
	})
}

// exportProgressView — модель страницы прогресса задания. Поля уже готовы к показу:
// шаблон не решает и не форматирует.
type exportProgressView struct {
	ID              string
	StatusLabel     string
	StatusClass     string
	Progress        int
	Step            string
	StartedLabel    string
	UpdatedLabel    string
	Terminal        bool
	Failed          bool
	Error           string
	ErrorCode       string
	ResultURL       string
	ResultAvailable bool
	CanRetry        bool
	FragmentURL     string
	TenantID        string
	Live            components.Live
}

// jobRow — строка DLQ: идентификатор, число попыток, нормализованная причина и
// момент последнего изменения.
type jobRow struct {
	ID           string
	Attempts     int
	Reason       string
	UpdatedLabel string
}

// jobsView — модель админ-экрана заданий.
type jobsView struct {
	Rows    []jobRow
	Empty   bool
	Retried string
	Live    components.Live
}

// exportProgressViewOf собирает модель прогресса из операционной записи задания.
func exportProgressViewOf(record ports.ReportExport, canManageJobs bool, ctx components.ConsoleContext) exportProgressView {
	view := exportProgressView{
		ID:              record.ID,
		StatusLabel:     exportStatusLabel(record.Status),
		StatusClass:     components.StatusClass(string(record.Status)),
		Progress:        record.Progress,
		Step:            record.Step,
		Terminal:        record.Status.Terminal(),
		Failed:          record.Status == jobs.StatusFailed,
		Error:           record.Error,
		ErrorCode:       record.ErrorCode,
		ResultAvailable: record.ResultAvailable,
		CanRetry:        record.Status == jobs.StatusFailed,
		FragmentURL:     exportProgressPath(record.ID),
		TenantID:        ctx.TenantID,
		Live:            components.Live{ID: "console-live"},
	}
	if record.ResultAvailable {
		view.ResultURL = exportResultPath(record.ID)
	}
	formatter := components.DefaultFormatter()
	if !record.StartedAt.IsZero() {
		view.StartedLabel = formatter.DateTime(record.StartedAt.UTC())
	}
	if !record.UpdatedAt.IsZero() {
		view.UpdatedLabel = formatter.DateTime(record.UpdatedAt.UTC())
	}
	return view
}

// canManageJobs сообщает, доступен ли роли раздел заданий (DLQ). Решение — за
// портом доступа; недоступность порта не открывает доступ.
func (s *Server) canManageJobs(r *http.Request) bool {
	if s.deps.Access == nil {
		return false
	}
	allowed, err := s.deps.Access.Allow(r.Context(), roleOf(r), ports.ResourceJobs)
	return err == nil && allowed
}

// exportStatusLabel переводит состояние задания в формулировку каталога.
func exportStatusLabel(status jobs.Status) string {
	messages := consoleMessages()
	switch status {
	case jobs.StatusQueued:
		return messages.Text(msgExportStatusQueued)
	case jobs.StatusRunning:
		return messages.Text(msgExportStatusRunning)
	case jobs.StatusDone:
		return messages.Text(msgExportStatusDone)
	case jobs.StatusFailed:
		return messages.Text(msgExportStatusFailed)
	case jobs.StatusCancelled:
		return messages.Text(msgExportStatusCancel)
	default:
		return string(status)
	}
}

// renderFragment отдаёт именованный фрагмент набора страницы без каркаса: HTMX
// подставляет его в уже открытую страницу.
func (s *Server) renderFragment(w http.ResponseWriter, page, fragment string, data any) {
	if s.pages == nil {
		http.Error(w, "шаблоны консоли не собраны", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := s.pages.RenderFragment(&buf, page, fragment, data); err != nil {
		http.Error(w, "ошибка рендеринга фрагмента", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// isHTMX сообщает, что запрос пришёл от HTMX (частичное обновление).
func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }
