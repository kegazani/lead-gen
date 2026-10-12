package httpapi_test

import (
	"bytes"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

const (
	requestID = "req-1"
	tenantID  = "tenant-1"
)

// newTestServer собирает HTTP-сервер среза поверх памяти и заданного
// query-контракта артефактов.
func newTestServer(t *testing.T, query ports.FulfillmentQuery) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		Query:           query,
		ConsoleUser:     consoleUser,
		ConsolePassword: consolePassword,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// TestRootRedirectsToValidation проверяет, что корень сервиса ведёт на очередь
// валидации, а не отвечает 404.
func TestRootRedirectsToValidation(t *testing.T) {
	server := newTestServer(t, fulfillment.NewMemoryQuery())
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
	if location := resp.Header.Get("Location"); location != "/validation" {
		t.Fatalf("Location = %q, ожидался /validation", location)
	}
}

// TestValidationLinksSharedStylesheet проверяет, что экран заявок подключает
// общее оформление компонентов и оно реально раздаётся.
func TestValidationLinksSharedStylesheet(t *testing.T) {
	server := newTestServer(t, fulfillment.NewMemoryQuery())
	client := newConsoleClient(server)
	client.login(t)

	page := readBody(t, client.get(t, "/validation"))
	if !strings.Contains(page, `<link rel="stylesheet" href="/static/console.css">`) {
		t.Fatalf("экран не подключил общее оформление:\n%s", page)
	}
	// Статика публична: отдаётся без сессии.
	if css := do(t, server, http.MethodGet, "/static/console.css", nil); css.StatusCode != http.StatusOK {
		t.Fatalf("оформление не раздаётся: статус %d", css.StatusCode)
	}
}

// TestHTTPLifecycle — интеграционная проверка обработчиков: создание заявки,
// чтение статуса, команды переходов и чтение статусов артефактов.
func TestHTTPLifecycle(t *testing.T) {
	memory := fulfillment.NewMemoryQuery().Set(
		ports.ArtifactRef{Kind: ports.ArtifactAudienceSnapshot, ID: "snap-1"}, "fixed", true,
	)
	server := newTestServer(t, memory)

	// Создание заявки.
	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"title": "Автоматизация найма", "actor": "actor-1", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /requests = %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Чтение статуса.
	view := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"?tenant_id="+tenantID, nil))
	if view["Status"] != "submitted" {
		t.Fatalf("статус = %v", view["Status"])
	}

	// Переходы статуса.
	for _, path := range []string{"validation/start", "validation/complete", "processing/start"} {
		resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/"+path, transition(tenantID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", path, resp.StatusCode, readBody(t, resp))
		}
	}

	// Фиксация ссылки на аудиторию.
	resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/artifacts/audience-snapshot",
		map[string]any{"tenant_id": tenantID, "id": "snap-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("фиксация аудитории = %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Чтение статусов артефактов через query-контракт.
	status := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"/fulfillment-status?tenant_id="+tenantID, nil))
	if status["Status"] != "processing" {
		t.Fatalf("статус заявки = %v", status["Status"])
	}
	artifacts, ok := status["Artifacts"].([]any)
	if !ok || len(artifacts) != 1 {
		t.Fatalf("артефакты = %v", status["Artifacts"])
	}
	first, _ := artifacts[0].(map[string]any)
	if first["Status"] != "fixed" || first["Found"] != true {
		t.Fatalf("статус артефакта = %v", first)
	}

	// Завершение.
	resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/fulfillment", transition(tenantID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fulfillment = %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

// TestHTTPErrors — отображение ошибок: пустой оффер → 400, неизвестная заявка →
// 404, запрещённый переход → 409.
func TestHTTPErrors(t *testing.T) {
	server := newTestServer(t, fulfillment.NewMemoryQuery())

	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"title": "Пустой оффер", "actor": "actor-1", "content": "   ",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("пустой оффер = %d, ожидался 400", resp.StatusCode)
	}

	resp = do(t, server, http.MethodGet, "/requests/unknown?tenant_id="+tenantID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестная заявка = %d, ожидался 404", resp.StatusCode)
	}

	resp = do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"title": "Проверка ошибок", "actor": "actor-1", "content": "Оффер",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание = %d", resp.StatusCode)
	}

	resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/processing/start", transition(tenantID))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("запуск без валидации = %d, ожидался 409", resp.StatusCode)
	}
}

// TestHTTPFulfillmentUnavailable — отказ чтения контракта артефактов отдаётся как
// 502, а не как выдуманный статус.
func TestHTTPFulfillmentUnavailable(t *testing.T) {
	server := newTestServer(t, nil)
	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"title": "Недоступный фулфилмент", "actor": "actor-1", "content": "Оффер",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание = %d", resp.StatusCode)
	}
	resp = do(t, server, http.MethodPost, "/requests/"+requestID+"/artifacts/campaign",
		map[string]any{"tenant_id": tenantID, "id": "camp-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("фиксация кампании = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp = do(t, server, http.MethodGet, "/requests/"+requestID+"/fulfillment-status?tenant_id="+tenantID, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("недоступный контракт = %d, ожидался 502", resp.StatusCode)
	}
}

// transition собирает тело команды перехода.
func transition(tenant string) map[string]any {
	return map[string]any{"tenant_id": tenant, "actor": "actor-1"}
}

// do выполняет запрос к тестовому серверу.
func do(t *testing.T, server *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("сериализация тела: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("выполнение запроса: %v", err)
	}
	return response
}

// readBody читает тело ответа для диагностики.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	resp.Body.Close()
	return string(data)
}

// decodeView разбирает JSON-ответ в карту.
func decodeView(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	return out
}

// visibleText возвращает видимый текст страницы: теги заменяются пробелом,
// пробельные символы схлопываются. Экраны показывают сведения парами «подпись —
// значение» в разных элементах (dt/dd), поэтому проверка «Найдено лидов: 7»
// идёт по тексту, который видит оператор, а не по разметке.
func visibleText(page string) string {
	withoutTags := regexp.MustCompile(`(?s)<[^>]*>`).ReplaceAllString(page, " ")
	return strings.Join(strings.Fields(html.UnescapeString(withoutTags)), " ")
}
