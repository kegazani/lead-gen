package httpapi_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// seedReports наполняет проекцию сценарием из двух кампаний и одного узкого
// среза, чтобы проверить отчётность и порог минимальной группы.
func seedReports(t *testing.T, server *httptest.Server) {
	t.Helper()
	occurred := day(0).Format(time.RFC3339)
	events := []map[string]any{
		{"tenant_id": "t1", "company_id": "company-a", "kind": "lead-workflow", "status": "warming", "source": "web", "industry": "IT", "segment": "smb", "campaign_id": "camp-1", "manager_id": "mg-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-a", "kind": "message-sent", "campaign_id": "camp-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-a", "kind": "delivery-event", "outcome": "delivered", "campaign_id": "camp-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-a", "kind": "engagement-observed", "outcome": "clicked", "campaign_id": "camp-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-a", "kind": "engagement-observed", "outcome": "replied", "campaign_id": "camp-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-a", "kind": "lead-outcome", "outcome": "won", "campaign_id": "camp-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-a", "kind": "email-verification", "outcome": "valid", "campaign_id": "camp-1", "occurred_at": occurred},

		{"tenant_id": "t1", "company_id": "company-b", "kind": "lead-workflow", "status": "warming", "source": "web", "industry": "IT", "segment": "smb", "campaign_id": "camp-1", "manager_id": "mg-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-b", "kind": "message-sent", "campaign_id": "camp-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-b", "kind": "delivery-event", "outcome": "bounced", "campaign_id": "camp-1", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-b", "kind": "email-verification", "outcome": "invalid", "campaign_id": "camp-1", "occurred_at": occurred},

		{"tenant_id": "t1", "company_id": "company-c", "kind": "lead-workflow", "status": "warming", "source": "referral", "industry": "finance", "segment": "ent", "campaign_id": "camp-2", "manager_id": "mg-2", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-c", "kind": "message-sent", "campaign_id": "camp-2", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "company-c", "kind": "engagement-observed", "outcome": "unsubscribed", "campaign_id": "camp-2", "occurred_at": occurred},

		{"tenant_id": "t1", "company_id": "company-d", "kind": "lead-workflow", "status": "duplicate", "source": "referral", "industry": "finance", "segment": "ent", "campaign_id": "camp-2", "manager_id": "mg-2", "occurred_at": occurred},

		// Узкий срез из одной записи: должен быть скрыт порогом группы.
		{"tenant_id": "t1", "company_id": "company-e", "kind": "lead-workflow", "status": "warming", "source": "solo", "industry": "IT", "segment": "smb", "campaign_id": "camp-3", "manager_id": "mg-3", "occurred_at": occurred},
	}
	for _, event := range events {
		postEvent(t, server, event)
	}
}

// TestReportsRendersAllSections проверяет экран отчётности: кампании,
// лидогенерацию, воронку, качество базы, управленческий раздел и время
// обновления. Метрик открытий нет.
func TestReportsRendersAllSections(t *testing.T) {
	server := newServer(t, day(10))
	seedReports(t, server)

	status, page := getPage(t, server, "/reports?tenant_id=t1")
	if status != http.StatusOK {
		t.Fatalf("GET /reports = %d, ожидался 200", status)
	}
	for _, want := range []string{"Отчётность", "Кампании", "Лидогенерация", "Воронка", "Качество базы", "Управленческая отчётность", "Обновлено:", "camp-1", "camp-2"} {
		if !strings.Contains(page, want) {
			t.Fatalf("экран отчётности не содержит %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "camp-3") {
		t.Fatalf("группа ниже порога не должна попадать на экран:\n%s", page)
	}
	if strings.Contains(strings.ToLower(page), "open") || strings.Contains(page, "открыт") {
		t.Fatalf("экран отчётности не должен содержать метрик открытий:\n%s", page)
	}
	if strings.Contains(page, "company-a") || strings.Contains(page, "company-b") {
		t.Fatalf("экран не должен раскрывать записи пула:\n%s", page)
	}
}

// TestReportsHiddenSlice проверяет порог минимальной группы в HTTP-слое: узкий
// срез кампании не показывается и причина сообщается.
func TestReportsHiddenSlice(t *testing.T) {
	server := newServer(t, day(10))
	seedReports(t, server)

	status, page := getPage(t, server, "/reports?tenant_id=t1&campaign_id=camp-3")
	if status != http.StatusOK {
		t.Fatalf("GET /reports = %d, ожидался 200", status)
	}
	if !strings.Contains(page, "недостаточный размер группы") {
		t.Fatalf("узкий срез должен сообщать причину скрытия:\n%s", page)
	}
	if strings.Contains(page, "<td>camp-3</td>") {
		t.Fatalf("скрытый срез не должен показывать метрики кампании:\n%s", page)
	}
	if strings.Contains(page, `data-metric="sent"`) {
		t.Fatalf("скрытый срез не должен содержать таблицу метрик:\n%s", page)
	}
}

// TestReportsFilterByPeriod проверяет фильтр по периоду: вне периода срез скрыт.
func TestReportsFilterByPeriod(t *testing.T) {
	server := newServer(t, day(10))
	seedReports(t, server)

	status, page := getPage(t, server, "/reports?tenant_id=t1&from=2030-01-01&to=2030-01-31")
	if status != http.StatusOK {
		t.Fatalf("GET /reports = %d, ожидался 200", status)
	}
	if !strings.Contains(page, "недостаточный размер группы") {
		t.Fatalf("пустой период должен скрывать срез:\n%s", page)
	}
}

// TestReportsRejectsBadPeriod проверяет, что некорректная граница периода
// отвергается, а не игнорируется.
func TestReportsRejectsBadPeriod(t *testing.T) {
	server := newServer(t, day(10))
	if status, _ := getPage(t, server, "/reports?tenant_id=t1&from=not-a-date"); status != http.StatusBadRequest {
		t.Fatalf("GET /reports с плохим периодом = %d, ожидался 400", status)
	}
}

// TestReportsAccessDenied проверяет, что отчётность закрыта для роли без прав.
func TestReportsAccessDenied(t *testing.T) {
	server := newServer(t, day(10))
	if status, _ := getPage(t, server, "/reports?tenant_id=t1&role=guest"); status != http.StatusForbidden {
		t.Fatalf("GET /reports для чужой роли = %d, ожидался 403", status)
	}
	if status, _ := getPage(t, server, "/reports/export.csv?tenant_id=t1&role=guest"); status != http.StatusForbidden {
		t.Fatalf("GET /reports/export.csv для чужой роли = %d, ожидался 403", status)
	}
}

// TestReportsExportCSV проверяет выгрузку CSV: BOM, разделы отчёта и отсутствие
// записей пула.
func TestReportsExportCSV(t *testing.T) {
	server := newServer(t, day(10))
	seedReports(t, server)

	resp, err := http.Get(server.URL + "/reports/export.csv?tenant_id=t1")
	if err != nil {
		t.Fatalf("GET CSV: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET CSV = %d, ожидался 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, ожидался text/csv", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Fatalf("Content-Disposition = %q, ожидалось вложение .csv", cd)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение CSV: %v", err)
	}
	body := string(data)
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatalf("CSV должен начинаться с BOM")
	}
	for _, want := range []string{"Кампании", "camp-1", "Воронка", "Качество базы", "Менеджеры", "mg-1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("CSV не содержит %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "camp-3") {
		t.Fatalf("узкий срез не должен попадать в выгрузку:\n%s", body)
	}
	if strings.Contains(body, "company-a") || strings.Contains(body, "company-b") {
		t.Fatalf("выгрузка не должна раскрывать записи пула:\n%s", body)
	}
	if strings.Contains(strings.ToLower(body), "open") || strings.Contains(body, "открыт") {
		t.Fatalf("выгрузка не должна содержать метрик открытий:\n%s", body)
	}
}

// TestReportsExportXLSX проверяет выгрузку XLSX: тип ответа и содержимое таблицы.
func TestReportsExportXLSX(t *testing.T) {
	server := newServer(t, day(10))
	seedReports(t, server)

	resp, err := http.Get(server.URL + "/reports/export.xlsx?tenant_id=t1")
	if err != nil {
		t.Fatalf("GET XLSX: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET XLSX = %d, ожидался 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("Content-Type = %q, ожидался spreadsheetml", ct)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение XLSX: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("XLSX не является zip-архивом: %v", err)
	}
	sheet := readZipPart(t, reader, "xl/worksheets/sheet1.xml")
	for _, want := range []string{"Кампании", "camp-1", "Менеджеры"} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("лист XLSX не содержит %q:\n%s", want, sheet)
		}
	}
}

// TestReportsExportHonestWithoutData проверяет честную выгрузку при отсутствии
// видимых данных: файл не подменяет отсутствующие метрики нулями.
func TestReportsExportHonestWithoutData(t *testing.T) {
	server := newServer(t, day(10))

	resp, err := http.Get(server.URL + "/reports/export.csv?tenant_id=empty")
	if err != nil {
		t.Fatalf("GET CSV: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	body := string(data)
	if !strings.Contains(body, "не показан") {
		t.Fatalf("выгрузка пустого среза должна сообщать причину:\n%s", body)
	}
	if strings.Contains(body, "Кампании") {
		t.Fatalf("пустой срез не должен содержать таблиц метрик:\n%s", body)
	}
}

// readZipPart читает часть архива как строку.
func readZipPart(t *testing.T, reader *zip.Reader, name string) string {
	t.Helper()
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatalf("открытие части %s: %v", name, err)
		}
		defer stream.Close()
		data, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("чтение части %s: %v", name, err)
		}
		return string(data)
	}
	t.Fatalf("часть %s не найдена в XLSX", name)
	return ""
}
