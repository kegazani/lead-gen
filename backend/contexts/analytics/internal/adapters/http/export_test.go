package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	analytics "lead-gen/backend/contexts/analytics"
	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
)

// newExportEnv собирает срез и HTTP-сервер, отдавая и сам срез: тесту нужен
// доступ к хранилищу заданий и обработчику, чтобы провести фоновый прогон роли
// worker вручную (как это делал бы jobs.Runner).
func newExportEnv(t *testing.T, now time.Time) (*httptest.Server, *analytics.Slice) {
	t.Helper()
	return newExportEnvWithSource(t, now, nil)
}

// newExportEnvWithSource собирает срез с заданным источником агрегатов отчёта:
// nil — расчёт по read-модели витрины, иной источник позволяет проверить отказ.
func newExportEnvWithSource(t *testing.T, now time.Time, source query.ReportSource) (*httptest.Server, *analytics.Slice) {
	t.Helper()
	slice, err := analytics.Build(analytics.Config{
		Clock:        fakeClock{now: now},
		StaleDays:    7,
		ReportSource: source,
		Logger:       observ.NewLoggerWithWriter(io.Discard),
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server, slice
}

// unavailableSource — источник агрегатов, который честно сообщает о недоступности
// контура клиента.
type unavailableSource struct{}

func (unavailableSource) Report(string, readmodel.ReportFilter) (readmodel.ReportData, error) {
	return readmodel.ReportData{}, query.ErrReportSourceUnavailable
}

// exportTicket — разобранный ответ постановки длительной выгрузки.
type exportTicket struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url"`
	ResultURL string `json:"result_url"`
}

// enqueueExport ставит выгрузку и возвращает билет задания.
func enqueueExport(t *testing.T, server *httptest.Server, query string) exportTicket {
	t.Helper()
	resp, err := http.Post(server.URL+"/reports/exports?"+query, "application/json", nil)
	if err != nil {
		t.Fatalf("POST /reports/exports: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /reports/exports = %d, ожидался 202", resp.StatusCode)
	}
	var ticket exportTicket
	if err := json.NewDecoder(resp.Body).Decode(&ticket); err != nil {
		t.Fatalf("разбор билета задания: %v", err)
	}
	return ticket
}

// processExportJob прогоняет одно задание выгрузки через тот же примитив jobs,
// которым пользуется роль worker.
func processExportJob(t *testing.T, slice *analytics.Slice) {
	t.Helper()
	runner := &jobs.Runner{
		Store: slice.ExportStore, Handler: slice.ExportReport.Handle,
		FailureNormalizer: jobs.NewCatalogFailureNormalizer(map[string]jobs.FailureRule{
			"export.payload.invalid":    {Message: "Некорректный запрос выгрузки", Diagnostic: "invalid_payload"},
			"export.report.unavailable": {Message: "Отчёт недоступен: контур клиента не отвечает", Diagnostic: "report_unavailable"},
		}, jobs.Failure{}),
	}
	processed, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("RunOnce обработал %d заданий, ожидалось 1", processed)
	}
}

// TestExportEnqueueReturnsJobIDWithoutFile проверяет критерий 3.4: запрос выгрузки
// возвращает идентификатор задания, не блокируется и не отдаёт готовый файл сразу —
// ссылка на результат до завершения задания не действует.
func TestExportEnqueueReturnsJobIDWithoutFile(t *testing.T) {
	server, _ := newExportEnv(t, day(10))
	seedReports(t, server)

	ticket := enqueueExport(t, server, "tenant_id=t1&format=csv")
	if strings.TrimSpace(ticket.JobID) == "" {
		t.Fatalf("постановка не вернула идентификатор задания: %+v", ticket)
	}
	if ticket.Status != string(jobs.StatusQueued) {
		t.Fatalf("статус нового задания = %q, ожидался %q", ticket.Status, jobs.StatusQueued)
	}
	if ticket.ResultURL == "" || ticket.StatusURL == "" {
		t.Fatalf("постановка не вернула ссылки на статус и результат: %+v", ticket)
	}

	// Ответ постановки — билет, а не файл: метрик отчёта в нём нет.
	if strings.Contains(ticket.ResultURL, "camp-1") {
		t.Fatalf("ссылка на результат не должна нести данные файла: %q", ticket.ResultURL)
	}

	// Сразу после постановки результата ещё нет: ссылка отвечает конфликтом.
	resp, err := http.Get(server.URL + ticket.ResultURL)
	if err != nil {
		t.Fatalf("GET %s: %v", ticket.ResultURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("GET результата до выполнения = %d, ожидался 409", resp.StatusCode)
	}
}

// TestExportJobProducesRealResult проверяет критерий 3.4 со стороны результата:
// после выполнения задания ссылка отдаёт реальный сформированный файл выгрузки, а
// статус наблюдаем и помечает результат готовым.
func TestExportJobProducesRealResult(t *testing.T) {
	server, slice := newExportEnv(t, day(10))
	seedReports(t, server)

	ticket := enqueueExport(t, server, "tenant_id=t1&format=csv")
	processExportJob(t, slice)

	// Статус задания наблюдаем: done и результат готов.
	statusResp, err := http.Get(server.URL + ticket.StatusURL)
	if err != nil {
		t.Fatalf("GET статуса: %v", err)
	}
	defer statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("GET статуса = %d, ожидался 200", statusResp.StatusCode)
	}
	var status exportStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatalf("разбор статуса: %v", err)
	}
	if status.Status != string(jobs.StatusDone) {
		t.Fatalf("статус задания = %q, ожидался %q", status.Status, jobs.StatusDone)
	}
	if !status.ResultAvailable || status.ResultURL == "" {
		t.Fatalf("готовый результат не помечен доступным: %+v", status)
	}

	// Ссылка отдаёт реальный файл: CSV с BOM, разделами отчёта и без записей пула.
	resultResp, err := http.Get(server.URL + status.ResultURL)
	if err != nil {
		t.Fatalf("GET результата: %v", err)
	}
	defer resultResp.Body.Close()
	if resultResp.StatusCode != http.StatusOK {
		t.Fatalf("GET результата = %d, ожидался 200", resultResp.StatusCode)
	}
	if ct := resultResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, ожидался text/csv", ct)
	}
	if cd := resultResp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Fatalf("Content-Disposition = %q, ожидалось вложение .csv", cd)
	}
	data, err := io.ReadAll(resultResp.Body)
	if err != nil {
		t.Fatalf("чтение файла: %v", err)
	}
	body := string(data)
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatalf("файл выгрузки должен начинаться с BOM")
	}
	for _, want := range []string{"Кампании", "camp-1", "Воронка", "Качество базы"} {
		if !strings.Contains(body, want) {
			t.Fatalf("файл выгрузки не содержит %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "company-a") {
		t.Fatalf("файл выгрузки не должен раскрывать записи пула:\n%s", body)
	}
}

// TestExportFailedJobReportsNormalizedReason проверяет критерий 3.4 со стороны
// отказа: неуспешное задание отдаёт нормализованную причину без технических
// деталей, а ссылка на результат отвечает конфликтом с той же причиной.
func TestExportFailedJobReportsNormalizedReason(t *testing.T) {
	server, slice := newExportEnv(t, day(10))

	// Испорченный Payload — модель «задание, чей разбор провалился». Техническая
	// деталь разбора (например, «invalid character») не должна покинуть сервис.
	if err := slice.ExportStore.Create(context.Background(), jobs.Job{
		ID:      "broken-export",
		Payload: []byte("не json"),
	}); err != nil {
		t.Fatalf("подготовка задания: %v", err)
	}
	processExportJob(t, slice)

	statusResp, err := http.Get(server.URL + "/reports/exports/broken-export")
	if err != nil {
		t.Fatalf("GET статуса: %v", err)
	}
	defer statusResp.Body.Close()
	var status exportStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatalf("разбор статуса: %v", err)
	}
	if status.Status != string(jobs.StatusFailed) {
		t.Fatalf("статус задания = %q, ожидался %q", status.Status, jobs.StatusFailed)
	}
	if status.Error != "Некорректный запрос выгрузки" {
		t.Fatalf("причина отказа = %q, ожидалась нормализованная формулировка", status.Error)
	}
	if status.ErrorCode != "export.payload.invalid" {
		t.Fatalf("код отказа = %q, ожидался export.payload.invalid", status.ErrorCode)
	}
	if strings.Contains(strings.ToLower(status.Error), "invalid") || strings.Contains(status.Error, "json") {
		t.Fatalf("нормализованная причина не должна нести технических деталей: %q", status.Error)
	}

	// Ссылка на результат для неуспешного задания отвечает конфликтом и той же
	// нормализованной причиной, а не пустым файлом.
	resultResp, err := http.Get(server.URL + "/reports/exports/broken-export/result")
	if err != nil {
		t.Fatalf("GET результата: %v", err)
	}
	defer resultResp.Body.Close()
	if resultResp.StatusCode != http.StatusConflict {
		t.Fatalf("GET результата упавшего задания = %d, ожидался 409", resultResp.StatusCode)
	}
	reason, _ := io.ReadAll(resultResp.Body)
	if !strings.Contains(string(reason), "Некорректный запрос выгрузки") {
		t.Fatalf("результат упавшего задания не несёт нормализованную причину: %q", string(reason))
	}
}

// TestExportUnknownFormatRejected проверяет, что неизвестный формат отклоняется на
// постановке, а не превращается в задание, которое заведомо не соберётся.
func TestExportUnknownFormatRejected(t *testing.T) {
	server, _ := newExportEnv(t, day(10))
	resp, err := http.Post(server.URL+"/reports/exports?tenant_id=t1&format=pdf", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /reports/exports: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST с неизвестным форматом = %d, ожидался 400", resp.StatusCode)
	}
}

// TestExportUnavailableSourceReportsNormalizedReason проверяет отказ по реальной
// причине: недоступный контур отчёта даёт неуспешное задание с нормализованной
// формулировкой и машинным кодом, без технических деталей.
func TestExportUnavailableSourceReportsNormalizedReason(t *testing.T) {
	server, slice := newExportEnvWithSource(t, day(10), unavailableSource{})

	ticket := enqueueExport(t, server, "tenant_id=t1&format=csv")
	processExportJob(t, slice)

	statusResp, err := http.Get(server.URL + ticket.StatusURL)
	if err != nil {
		t.Fatalf("GET статуса: %v", err)
	}
	defer statusResp.Body.Close()
	var status exportStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatalf("разбор статуса: %v", err)
	}
	if status.Status != string(jobs.StatusFailed) {
		t.Fatalf("статус задания = %q, ожидался %q", status.Status, jobs.StatusFailed)
	}
	if status.Error != "Отчёт недоступен: контур клиента не отвечает" {
		t.Fatalf("причина отказа = %q, ожидалась нормализованная формулировка", status.Error)
	}
	if status.ErrorCode != "export.report.unavailable" {
		t.Fatalf("код отказа = %q, ожидался export.report.unavailable", status.ErrorCode)
	}
}

// exportStatus — разобранный статус задания выгрузки.
type exportStatus struct {
	JobID           string `json:"job_id"`
	Status          string `json:"status"`
	Error           string `json:"error"`
	ErrorCode       string `json:"error_code"`
	ResultAvailable bool   `json:"result_available"`
	ResultURL       string `json:"result_url"`
}
