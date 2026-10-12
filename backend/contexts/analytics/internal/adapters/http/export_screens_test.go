package httpapi_test

import (
	"context"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	analytics "lead-gen/backend/contexts/analytics"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
)

// exportTimeRe — отформатированная дата-время в разметке. Момент последнего
// изменения задания берётся по реальным часам (хранилище не инжектится), поэтому в
// золотых файлах он нормализуется до TIME.
var exportTimeRe = regexp.MustCompile(`\d{2}\.\d{2}\.\d{4} \d{2}:\d{2}`)

// updateExportGolden — при true перезаписывает золотые файлы экранов длительных
// операций текущим выводом.
var updateExportGolden = flag.Bool("update", false, "перезаписать золотые файлы экранов длительных операций")

// validExportPayload собирает корректный Payload задания выгрузки для тенанта t1.
func validExportPayload(t *testing.T) []byte {
	t.Helper()
	payload, err := ports.EncodeReportExportPayload(ports.ReportExportPayloadFromRequest(ports.ReportExportRequest{
		TenantID: "t1",
		Format:   ports.ExportCSV,
	}))
	if err != nil {
		t.Fatalf("кодирование payload: %v", err)
	}
	return payload
}

// seedExportJob кладёт задание с фиксированным идентификатором и моментом
// постановки, чтобы разметка была детерминирована.
func seedExportJob(t *testing.T, slice *analytics.Slice, id, payload string, attempts int, createdAt time.Time) {
	t.Helper()
	if err := slice.ExportStore.Create(context.Background(), jobs.Job{
		ID:        id,
		Payload:   []byte(payload),
		Attempts:  attempts,
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("подготовка задания %s: %v", id, err)
	}
}

// getHTMX выполняет GET с заголовком HTMX-запроса и возвращает код и тело.
func getHTMX(t *testing.T, server string, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server+path, nil)
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	return resp.StatusCode, string(data)
}

// TestExportEnqueueHTMLRedirectsToProgress проверяет критерий 6.1: действие
// возвращает идентификатор задания и страницу прогресса (POST/redirect/GET), а не
// блокируется на сборке файла.
func TestExportEnqueueHTMLRedirectsToProgress(t *testing.T) {
	server, _ := newExportEnv(t, day(10))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := client.PostForm(server.URL+"/reports/exports/html?tenant_id=t1&format=csv", url.Values{})
	if err != nil {
		t.Fatalf("POST /reports/exports/html: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /reports/exports/html = %d, ожидался 303", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/reports/exports/") || !strings.HasSuffix(location, "/progress") {
		t.Fatalf("Location = %q, ожидался путь страницы прогресса", location)
	}
}

// TestExportProgressPollsWhileRunning проверяет критерий 6.2: незавершённая
// страница прогресса несёт HTMX-опрос фрагмента, а запрос HTMX получает только
// фрагмент (контракт «полная страница или фрагмент»).
func TestExportProgressPollsWhileRunning(t *testing.T) {
	server, slice := newExportEnv(t, day(10))
	seedExportJob(t, slice, "job-run", string(validExportPayload(t)), 0, day(0))

	status, page := getPage(t, server, "/reports/exports/job-run/progress")
	if status != http.StatusOK {
		t.Fatalf("GET страницы прогресса = %d, ожидался 200", status)
	}
	for _, want := range []string{`hx-get="/reports/exports/job-run/progress"`, `hx-trigger="every 2s"`, "job-run"} {
		if !strings.Contains(page, want) {
			t.Fatalf("страница прогресса не содержит %q:\n%s", want, page)
		}
	}

	fstatus, fragment := getHTMX(t, server.URL, "/reports/exports/job-run/progress")
	if fstatus != http.StatusOK {
		t.Fatalf("GET фрагмента = %d, ожидался 200", fstatus)
	}
	if strings.Contains(fragment, "<!DOCTYPE html>") || strings.Contains(fragment, `<main class="page"`) {
		t.Fatalf("HTMX-запрос должен получить фрагмент, а не полную страницу:\n%s", fragment)
	}
	if !strings.Contains(fragment, `id="export-progress"`) {
		t.Fatalf("фрагмент не содержит область прогресса:\n%s", fragment)
	}
}

// TestExportProgressDoneShowsResult проверяет критерий 6.2: по завершении фрагмент
// заменяется результатом — ссылкой на готовый файл и без опроса.
func TestExportProgressDoneShowsResult(t *testing.T) {
	server, slice := newExportEnv(t, day(10))
	seedReports(t, server)
	seedExportJob(t, slice, "job-done-page", string(validExportPayload(t)), 0, day(0))
	processExportJob(t, slice)

	_, page := getPage(t, server, "/reports/exports/job-done-page/progress")
	if !strings.Contains(page, "/reports/exports/job-done-page/result") {
		t.Fatalf("страница завершённого задания не содержит ссылку на результат:\n%s", page)
	}
	if strings.Contains(page, `hx-trigger="every 2s"`) {
		t.Fatalf("завершённое задание не должно опрашиваться:\n%s", page)
	}

	fstatus, fragment := getHTMX(t, server.URL, "/reports/exports/job-done-page/progress")
	if fstatus != http.StatusOK {
		t.Fatalf("GET фрагмента завершённого = %d, ожидался 200", fstatus)
	}
	if !strings.Contains(fragment, "/reports/exports/job-done-page/result") || strings.Contains(fragment, "hx-trigger") {
		t.Fatalf("фрагмент завершённого задания не заменён результатом:\n%s", fragment)
	}
}

// TestExportProgressFailedShowsNormalizedReasonAndRetry проверяет критерий 6.3:
// неуспешное задание показывает нормализованную причину без технических деталей и
// форму повтора.
func TestExportProgressFailedShowsNormalizedReasonAndRetry(t *testing.T) {
	server, slice := newExportEnv(t, day(10))
	seedExportJob(t, slice, "job-broken", "не json", 0, day(0))
	processExportJob(t, slice)

	status, page := getPage(t, server, "/reports/exports/job-broken/progress")
	if status != http.StatusOK {
		t.Fatalf("GET страницы упавшего задания = %d, ожидался 200", status)
	}
	if !strings.Contains(page, "Некорректный запрос выгрузки") {
		t.Fatalf("страница не показывает нормализованную причину:\n%s", page)
	}
	lower := strings.ToLower(page)
	if strings.Contains(lower, "invalid character") || strings.Contains(page, "json") {
		t.Fatalf("причина не должна нести технических деталей:\n%s", page)
	}
	if !strings.Contains(page, `action="/reports/exports/job-broken/retry"`) {
		t.Fatalf("упавшее задание не предлагает повтор:\n%s", page)
	}

	// Повтор возвращает задание в очередь (PRG на страницу прогресса).
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(server.URL+"/reports/exports/job-broken/retry", url.Values{})
	if err != nil {
		t.Fatalf("POST повтора: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST повтора = %d, ожидался 303", resp.StatusCode)
	}
	job, err := slice.ExportStore.FindByID(context.Background(), "job-broken")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if job.Status != jobs.StatusQueued {
		t.Fatalf("после повтора задание в состоянии %q, ожидалось queued", job.Status)
	}
}

// TestJobsDLQAdminOnlyWithRetry проверяет критерий 6.4: DLQ виден администратору с
// причиной и позволяет инициировать повтор, а клиенту раздел закрыт.
func TestJobsDLQAdminOnlyWithRetry(t *testing.T) {
	server, slice := newExportEnv(t, day(10))
	seedJobFailed(t, slice, "dead-job", "Не удалось собрать файл выгрузки", 2)

	// Клиент (роль по умолчанию) не видит DLQ: доступ проверяется на сервере.
	if status, _ := getPage(t, server, "/reports/jobs"); status != http.StatusForbidden {
		t.Fatalf("GET /reports/jobs клиентом = %d, ожидался 403", status)
	}

	status, page := getPage(t, server, "/reports/jobs?role=admin")
	if status != http.StatusOK {
		t.Fatalf("GET /reports/jobs админом = %d, ожидался 200", status)
	}
	if !strings.Contains(page, "dead-job") || !strings.Contains(page, "Не удалось собрать файл выгрузки") {
		t.Fatalf("DLQ не показывает задание и причину:\n%s", page)
	}
	if !strings.Contains(page, `action="/reports/exports/dead-job/retry"`) {
		t.Fatalf("DLQ не предлагает повтор:\n%s", page)
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(server.URL+"/reports/exports/dead-job/retry?role=admin", url.Values{})
	if err != nil {
		t.Fatalf("POST повтора из DLQ: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST повтора из DLQ = %d, ожидался 303", resp.StatusCode)
	}
	job, err := slice.ExportStore.FindByID(context.Background(), "dead-job")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if job.Status != jobs.StatusQueued || job.Attempts != 0 {
		t.Fatalf("после повтора задание = %+v, ожидалось queued/0", job)
	}
}

// seedJobFailed кладёт задание в состоянии отказа с исчерпанными попытками.
func seedJobFailed(t *testing.T, slice *analytics.Slice, id, reason string, attempts int) {
	t.Helper()
	seedExportJob(t, slice, id, string(validExportPayload(t)), attempts, day(0))
	if err := slice.ExportStore.UpdateStatus(context.Background(), id, jobs.StatusQueued, jobs.StatusFailed, reason, "export.render.failed"); err != nil {
		t.Fatalf("перевод задания %s в отказ: %v", id, err)
	}
}

// TestExportProgressGolden фиксирует разметку страницы прогресса незавершённого
// задания: она не меняется незаметно.
func TestExportProgressGolden(t *testing.T) {
	server, slice := newExportEnv(t, day(10))
	seedExportJob(t, slice, "job-golden", string(validExportPayload(t)), 0, day(0))

	_, page := getPage(t, server, "/reports/exports/job-golden/progress")
	assertExportGolden(t, "export-progress.golden", page)
}

// TestJobsDLQGolden фиксирует разметку админ-экрана DLQ: задание, причина, повтор.
func TestJobsDLQGolden(t *testing.T) {
	server, slice := newExportEnv(t, day(10))
	seedJobFailed(t, slice, "dead-golden", "Не удалось собрать файл выгрузки", 2)

	_, page := getPage(t, server, "/reports/jobs?role=admin")
	assertExportGolden(t, "jobs-dlq.golden", page)
}

// assertExportGolden сравнивает разметку с золотым файлом; с флагом -update файл
// перезаписывается. Волатильные моменты времени нормализуются до TIME.
func assertExportGolden(t *testing.T, name, body string) {
	t.Helper()
	got := exportTimeRe.ReplaceAllString(body, "TIME")

	golden := filepath.Join("testdata", name)
	if *updateExportGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("создание testdata: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("запись золотого файла: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("чтение золотого файла %s: %v; запустите тест с -update для генерации", golden, err)
	}
	if got != string(want) {
		t.Fatalf("рендер не совпадает с золотым файлом %s\n--- получено ---\n%s\n--- ожидалось ---\n%s", golden, got, want)
	}
}
