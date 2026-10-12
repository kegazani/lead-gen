package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
)

// exportResponse — ответ постановки длительной выгрузки: идентификатор задания и
// ссылки на статус и результат. Готового файла здесь нет: файл появляется по
// ссылке на результат только после завершения задания.
type exportResponse struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url"`
	ResultURL string `json:"result_url"`
}

// exportStatusResponse — состояние задания выгрузки. Для отказа отдаётся
// нормализованная бизнес-причина и машинный код, без технических деталей.
type exportStatusResponse struct {
	JobID           string `json:"job_id"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	ResultAvailable bool   `json:"result_available"`
	ResultURL       string `json:"result_url,omitempty"`
}

// exportStatusPath и exportResultPath собирают ссылки на статус и результат
// задания. Путь относительный: сервис не обязан знать свой внешний адрес.
func exportStatusPath(id string) string { return "/reports/exports/" + id }

func exportResultPath(id string) string { return "/reports/exports/" + id + "/result" }

// handleReportExportEnqueue обслуживает POST /reports/exports: ставит длительную
// выгрузку в очередь и сразу возвращает идентификатор задания и ссылки, не
// собирая файл в этом запросе.
func (s *Server) handleReportExportEnqueue(w http.ResponseWriter, r *http.Request) {
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

	writeJSON(w, http.StatusAccepted, exportResponse{
		JobID:     record.ID,
		Status:    string(record.Status),
		StatusURL: exportStatusPath(record.ID),
		ResultURL: exportResultPath(record.ID),
	})
}

// handleReportExportStatus обслуживает GET /reports/exports/{id}: состояние
// задания и ссылка на результат, когда он готов.
func (s *Server) handleReportExportStatus(w http.ResponseWriter, r *http.Request) {
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
	response := exportStatusResponse{
		JobID:           record.ID,
		Status:          string(record.Status),
		Error:           record.Error,
		ErrorCode:       record.ErrorCode,
		ResultAvailable: record.ResultAvailable,
	}
	if record.ResultAvailable {
		response.ResultURL = exportResultPath(record.ID)
	}
	writeJSON(w, http.StatusOK, response)
}

// handleReportExportResult обслуживает GET /reports/exports/{id}/result: отдаёт
// готовый файл выгрузки. Недовыполненное задание отвечает конфликтом, а не пустым
// файлом; неуспешное — нормализованной причиной.
func (s *Server) handleReportExportResult(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	if s.deps.ExportJobs == nil {
		http.Error(w, "выгрузка отчёта не подключена", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	record, result, err := s.deps.ExportJobs.Result(r.Context(), id)
	switch {
	case errors.Is(err, ports.ErrReportExportNotFound):
		http.Error(w, "задание выгрузки не найдено", http.StatusNotFound)
		return
	case errors.Is(err, ports.ErrReportExportNoResult):
		http.Error(w, normalizeExportReason(record), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "чтение результата выгрузки", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+result.Filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Body)
}

// normalizeExportReason возвращает причину неготовности результата: для
// неуспешного задания — его нормализованную причину, иначе общее сообщение.
func normalizeExportReason(record ports.ReportExport) string {
	if record.Status == jobs.StatusFailed || record.Status == jobs.StatusCancelled {
		if reason := strings.TrimSpace(record.Error); reason != "" {
			return reason
		}
		return "Выгрузка не выполнена"
	}
	return "Результат выгрузки ещё не готов"
}

// parseExportFormat разбирает формат выгрузки из параметра запроса. Пустой
// параметр означает CSV — поведение прямой выгрузки.
func parseExportFormat(value string) (ports.ExportFormat, error) {
	format := ports.ExportFormat(strings.ToLower(strings.TrimSpace(value)))
	if format == "" {
		return ports.ExportCSV, nil
	}
	if !format.Valid() {
		return "", ports.ErrInvalidExportFormat
	}
	return format, nil
}
