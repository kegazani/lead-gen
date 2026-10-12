package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	analytics "lead-gen/backend/contexts/analytics"
)

// fakeClock — управляемые часы: возраст ожидания и среднее время детерминированы.
type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// day возвращает момент времени в UTC на заданный день от условной точки отсчёта.
func day(offset int) time.Time {
	return time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).AddDate(0, 0, offset)
}

// newServer собирает HTTP-сервер дашборда с управляемыми часами.
func newServer(t *testing.T, now time.Time) *httptest.Server {
	t.Helper()
	slice, err := analytics.Build(analytics.Config{Clock: fakeClock{now: now}, StaleDays: 7})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// postEvent отправляет нормализованное событие dev-приёмом.
func postEvent(t *testing.T, server *httptest.Server, event map[string]any) {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("сериализация события: %v", err)
	}
	resp, err := http.Post(server.URL+"/dashboard/events", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST события: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST события = %d, ожидался 202", resp.StatusCode)
	}
}

// seed заполняет дашборд сценарием из трёх компаний.
func seed(t *testing.T, server *httptest.Server) {
	t.Helper()
	occurred := day(0).Format(time.RFC3339)
	events := []map[string]any{
		{"tenant_id": "t1", "company_id": "c1", "kind": "lead-workflow", "status": "not warmed", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "c2", "kind": "lead-workflow", "status": "warming", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "c3", "kind": "lead-workflow", "status": "cold", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "c1", "kind": "message-sent", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "c2", "kind": "message-sent", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "c3", "kind": "message-sent", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "c1", "kind": "engagement-observed", "outcome": "replied", "occurred_at": occurred},
	}
	for _, event := range events {
		postEvent(t, server, event)
	}
}

// getPage выполняет GET и возвращает код и тело ответа.
func getPage(t *testing.T, server *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(server.URL + path)
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

// TestHealthz проверяет, что сервис отвечает 200 на проверку живости.
func TestHealthz(t *testing.T) {
	server := newServer(t, day(10))
	if status, _ := getPage(t, server, "/healthz"); status != http.StatusOK {
		t.Fatalf("GET /healthz = %d, ожидался 200", status)
	}
}

// TestRootRedirectsToDashboard проверяет, что корень сервиса ведёт на дашборд, а
// не отвечает 404.
func TestRootRedirectsToDashboard(t *testing.T) {
	server := newServer(t, day(10))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / = %d, ожидался 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "/dashboard" {
		t.Fatalf("Location = %q, ожидался /dashboard", location)
	}
}

// TestConsoleNavCanonicalWithoutGroups проверяет change console-shell-navigation
// (задачи 5.1–5.2, в части аналитики): общая навигация консоли отчётов состоит
// только из канонических разделов в порядке общего слоя, не несёт вторичных групп
// и отмечает активным ровно раздел «Отчёты» на своих экранах.
func TestConsoleNavCanonicalWithoutGroups(t *testing.T) {
	server := newServer(t, day(10))
	seed(t, server)

	for _, path := range []string{"/dashboard?tenant_id=t1", "/reports?tenant_id=t1"} {
		status, page := getPage(t, server, path)
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d, ожидался 200", path, status)
		}
		if strings.Contains(page, "sidebar-group-label") || strings.Contains(page, `<nav class="tabs">`) {
			t.Fatalf("экран %s не должен нести вторичную навигацию в сайдбаре или панели:\n%s", path, page)
		}
		// Разделы идут в каноническом порядке общего слоя.
		previous := -1
		for _, label := range []string{">Заявки</a>", ">Исполнение</a>", ">Лиды</a>", ">Отчёты</a>", ">Рассылки</a>"} {
			at := strings.Index(page, label)
			if at < 0 {
				t.Fatalf("экран %s не содержит раздел %q:\n%s", path, label, page)
			}
			if at < previous {
				t.Fatalf("порядок разделов на экране %s нарушен у %q:\n%s", path, label, page)
			}
			previous = at
		}
		// Активен ровно один раздел — «Отчёты».
		if active := strings.Count(page, `class="is-active"`); active != 1 {
			t.Fatalf("на экране %s активных разделов %d, ожидался 1:\n%s", path, active, page)
		}
		if !strings.Contains(page, `class="is-active" aria-current="page">Отчёты</a>`) {
			t.Fatalf("экран %s не отметил активным раздел «Отчёты»:\n%s", path, page)
		}
	}
}

// TestDashboardLinksSharedStylesheet проверяет, что экран подключает общее
// оформление компонентов и оно реально раздаётся.
func TestDashboardLinksSharedStylesheet(t *testing.T) {
	server := newServer(t, day(10))
	seed(t, server)

	_, page := getPage(t, server, "/dashboard?tenant_id=t1")
	if !strings.Contains(page, `<link rel="stylesheet" href="/static/console.css">`) {
		t.Fatalf("экран не подключил общее оформление:\n%s", page)
	}
	status, css := getPage(t, server, "/static/console.css")
	if status != http.StatusOK || !strings.Contains(css, ".page") {
		t.Fatalf("оформление не раздаётся: статус %d", status)
	}
}

// TestDashboardRendersLayers проверяет рендеринг обоих слоёв дашборда: ведущего
// (метрики прогресса) и операционного.
func TestDashboardRendersLayers(t *testing.T) {
	server := newServer(t, day(10))
	seed(t, server)

	status, page := getPage(t, server, "/dashboard?tenant_id=t1")
	if status != http.StatusOK {
		t.Fatalf("GET /dashboard = %d, ожидался 200", status)
	}
	if !strings.Contains(page, "<!DOCTYPE html>") {
		t.Fatalf("дашборд не отрисован как страница: %s", page)
	}
	leading := []string{"Найдено лидов", "Прокоммуницировано", "Ответили", "Прогретые", "Рост по дням", "Воронка"}
	for _, want := range leading {
		if !strings.Contains(page, want) {
			t.Fatalf("ведущий слой не содержит %q:\n%s", want, page)
		}
	}
	operations := []string{"В обработке", "Ожидают ответа", "Максимальный возраст ожидания", "Среднее время ожидания", "Холодные", "В очереди на добор"}
	for _, want := range operations {
		if !strings.Contains(page, want) {
			t.Fatalf("операционный слой не содержит %q:\n%s", want, page)
		}
	}
	if !strings.Contains(page, "growth-chart") {
		t.Fatalf("area-график роста не отрисован:\n%s", page)
	}
	if strings.Contains(strings.ToLower(page), "open") || strings.Contains(page, "Открыт") || strings.Contains(page, "открыт") {
		t.Fatalf("дашборд не должен содержать метрик открытий:\n%s", page)
	}
	if strings.Contains(page, "Срез скрыт") {
		t.Fatalf("для видимого среза не должно быть сообщения о скрытии:\n%s", page)
	}
}

// TestDashboardHiddenForSingleCompany проверяет порог минимальной группы в
// HTTP-слое: сужение до одной компании не показывает счётчики.
func TestDashboardHiddenForSingleCompany(t *testing.T) {
	server := newServer(t, day(10))
	seed(t, server)

	status, page := getPage(t, server, "/dashboard?tenant_id=t1&company_id=c1")
	if status != http.StatusOK {
		t.Fatalf("GET /dashboard = %d, ожидался 200", status)
	}
	if !strings.Contains(page, "Срез скрыт") {
		t.Fatalf("срез из одной записи должен быть скрыт:\n%s", page)
	}
	if strings.Contains(page, "Найдено лидов") {
		t.Fatalf("скрытый срез не должен показывать метрики:\n%s", page)
	}
}

// TestMaskingOverHTTP проверяет, что текст ответа не доходит до клиента ни на
// экране, ни в потоке.
func TestMaskingOverHTTP(t *testing.T) {
	server := newServer(t, day(10))
	seed(t, server)
	postEvent(t, server, map[string]any{
		"tenant_id": "t1", "company_id": "c2", "kind": "engagement-observed",
		"outcome": "replied", "reply_text": "SECRET-ANSWER", "occurred_at": day(1).Format(time.RFC3339),
	})

	_, page := getPage(t, server, "/dashboard?tenant_id=t1")
	if strings.Contains(page, "SECRET-ANSWER") {
		t.Fatalf("текст ответа утёк на дашборд:\n%s", page)
	}
}

// TestAccessDeniedForUnknownRole проверяет, что решение о доступе закрывает
// дашборд для роли без прав.
func TestAccessDeniedForUnknownRole(t *testing.T) {
	server := newServer(t, day(10))
	seed(t, server)

	if status, _ := getPage(t, server, "/dashboard?tenant_id=t1&role=guest"); status != http.StatusForbidden {
		t.Fatalf("GET /dashboard для чужой роли = %d, ожидался 403", status)
	}
	if status, _ := getPage(t, server, "/dashboard/stream?tenant_id=t1&role=guest"); status != http.StatusForbidden {
		t.Fatalf("GET /dashboard/stream для чужой роли = %d, ожидался 403", status)
	}
	if status, _ := getPage(t, server, "/dashboard?tenant_id=t1&role=client"); status != http.StatusOK {
		t.Fatalf("GET /dashboard для клиента = %d, ожидался 200", status)
	}
}

// TestStreamPushesUpdate проверяет живое обновление: после применения события
// подписчик получает свежие счётчики без ручного обновления страницы.
func TestStreamPushesUpdate(t *testing.T) {
	server := newServer(t, day(10))
	seed(t, server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/dashboard/stream?tenant_id=t1", nil)
	if err != nil {
		t.Fatalf("запрос потока: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("открытие потока: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, ожидался text/event-stream", ct)
	}
	reader := bufio.NewReader(resp.Body)

	initial := readFrame(t, reader)
	if initial.event != "metrics" {
		t.Fatalf("первый кадр = %q, ожидался metrics", initial.event)
	}
	if got := number(t, initial.data, "found"); got != 3 {
		t.Fatalf("found в первом кадре = %v, ожидалось 3", got)
	}
	if got := number(t, initial.data, "replied"); got != 1 {
		t.Fatalf("replied в первом кадре = %v, ожидалось 1", got)
	}

	// Применяем событие ответа без ручного обновления страницы.
	postEvent(t, server, map[string]any{
		"tenant_id": "t1", "company_id": "c2", "kind": "engagement-observed",
		"outcome": "replied", "occurred_at": day(1).Format(time.RFC3339),
	})

	updated := readFrame(t, reader)
	if got := number(t, updated.data, "replied"); got != 2 {
		t.Fatalf("replied после события = %v, ожидалось 2", got)
	}
}

// sseFrame — разобранный кадр потока серверных событий.
type sseFrame struct {
	event string
	data  map[string]any
}

// readFrame читает один кадр SSE с ограничением по времени, чтобы тест не завис
// при отсутствии обновления.
func readFrame(t *testing.T, reader *bufio.Reader) sseFrame {
	t.Helper()
	type outcome struct {
		frame sseFrame
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		frame := sseFrame{data: map[string]any{}}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				result <- outcome{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				result <- outcome{frame: frame}
				return
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				frame.event = value
			case "data":
				_ = json.Unmarshal([]byte(value), &frame.data)
			}
		}
	}()

	select {
	case out := <-result:
		if out.err != nil {
			t.Fatalf("чтение кадра потока: %v", out.err)
		}
		return out.frame
	case <-time.After(5 * time.Second):
		t.Fatalf("кадр потока не получен за 5с")
		return sseFrame{}
	}
}

// number извлекает числовое поле кадра.
func number(t *testing.T, data map[string]any, key string) float64 {
	t.Helper()
	value, ok := data[key]
	if !ok {
		t.Fatalf("в кадре нет поля %q: %v", key, data)
	}
	f, ok := value.(float64)
	if !ok {
		t.Fatalf("поле %q не число: %v", key, value)
	}
	return f
}
