package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/contracts/integration-platform/vector"
)

// fakeVectorPort — тестовый двойник контракта vector: детерминированный эмбеддинг
// и заданный набор совпадений. Уважает context.Context и предел k.
type fakeVectorPort struct {
	matches   []vector.Match
	searchErr error
}

// Embed реализует vector.Port.
func (f *fakeVectorPort) Embed(_ context.Context, text string) (vector.Vector, error) {
	return vector.Vector{float32(len(text))}, nil
}

// Upsert реализует vector.Port.
func (f *fakeVectorPort) Upsert(_ context.Context, document vector.Document) (vector.Vector, error) {
	return vector.Vector{float32(len(document.Text))}, nil
}

// Search реализует vector.Port: отдаёт заданные совпадения, усечённые до k.
func (f *fakeVectorPort) Search(_ context.Context, _ string, k int) ([]vector.Match, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if k > 0 && len(f.matches) > k {
		return f.matches[:k], nil
	}
	return f.matches, nil
}

// postJSON выполняет POST c JSON-телом, подставляя cookie сессии консоли.
func (c *consoleClient) postJSON(t *testing.T, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: c.session})
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// newSemanticRecallServer поднимает минимальную консоль с заданным сценарием recall
// (nil — сценарий не смонтирован) и оператором «operator / secret». Главная
// отрисовывается на in-memory репозиториях — из неё тест берёт CSRF-токен.
func newSemanticRecallServer(t *testing.T, useCase *app.SemanticRecall) *httptest.Server {
	t.Helper()
	server := httpapi.NewServer(httpapi.Dependencies{
		Showcase: &app.Showcase{
			Jobs:     memory.NewSearchJobStore(),
			Profiles: memory.NewLeadProfileRepository(),
			Contacts: memory.NewContactRepository(),
			Messages: memory.NewOutboundMessageRepository(),
		},
		SemanticRecall: useCase,
		Auth:           httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return ts
}

// recallCandidates — разобранный ответ recall.
type recallCandidates struct {
	Candidates []struct {
		CompanyID string  `json:"company_id"`
		Score     float64 `json:"score"`
	} `json:"candidates"`
}

// decodeRecallCandidates читает ответ recall.
func decodeRecallCandidates(t *testing.T, resp *http.Response) recallCandidates {
	t.Helper()
	defer resp.Body.Close()
	var payload recallCandidates
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("разбор ответа recall: %v", err)
	}
	return payload
}

// TestSemanticRecallHTTPOrdersCandidates проверяет путь оператора к recall: POST
// /semantic-recall отдаёт кандидатов, упорядоченных по убыванию близости, без
// вердикта релевантности.
func TestSemanticRecallHTTPOrdersCandidates(t *testing.T) {
	env := newTestEnv()
	env.vector.matches = []vector.Match{
		{ID: "a.example", Score: 0.91},
		{ID: "b.example", Score: 0.42},
	}
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postJSON(t, "/semantic-recall?csrf="+csrf, `{"text":"оффер: автоматизация продаж","limit":10}`)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("POST /semantic-recall = %d, ожидался 200: %s", resp.StatusCode, body)
	}
	candidates := decodeRecallCandidates(t, resp).Candidates
	if len(candidates) != 2 {
		t.Fatalf("кандидатов = %d, ожидалось 2", len(candidates))
	}
	if candidates[0].CompanyID != "a.example" || candidates[1].CompanyID != "b.example" {
		t.Fatalf("порядок кандидатов = %+v", candidates)
	}
	if candidates[0].Score < candidates[1].Score {
		t.Fatalf("список не упорядочен по убыванию близости: %+v", candidates)
	}
}

// TestSemanticRecallHTTPUnavailable проверяет честную недоступность: без
// векторного порта и без смонтированного сценария эндпоинт отвечает 503, а не
// пустым успехом с выдуманными кандидатами.
func TestSemanticRecallHTTPUnavailable(t *testing.T) {
	env := newTestEnv()
	env.recall.Vector = nil // порт не сконфигурирован
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postJSON(t, "/semantic-recall?csrf="+csrf, `{"text":"оффер"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /semantic-recall без порта = %d, ожидался 503", resp.StatusCode)
	}

	// Сценарий не смонтирован вовсе: тот же честный отказ.
	off := newConsoleClient(newSemanticRecallServer(t, nil))
	off.login(t, "operator", "secret")
	offCSRF := off.csrfForPage(t, "/")
	respOff := off.postJSON(t, "/semantic-recall?csrf="+offCSRF, `{"text":"оффер"}`)
	defer respOff.Body.Close()
	if respOff.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /semantic-recall без сценария = %d, ожидался 503", respOff.StatusCode)
	}
}

// TestSemanticRecallHTTPRejectsEmptyText проверяет, что пустой текст оффера
// отклоняется (400), а не даёт выдуманных кандидатов.
func TestSemanticRecallHTTPRejectsEmptyText(t *testing.T) {
	env := newTestEnv()
	env.vector.matches = []vector.Match{{ID: "a.example", Score: 0.9}}
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postJSON(t, "/semantic-recall?csrf="+csrf, `{"text":"   "}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /semantic-recall с пустым текстом = %d, ожидался 400", resp.StatusCode)
	}
}

// TestEmbedCompanyHTTP проверяет путь оператора к эмбеддингу компании: POST
// /cards/{id}/embed возвращает размерность вектора и watermark исходных данных.
func TestEmbedCompanyHTTP(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "acme.example", "Acme", "разработка ПО")
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postJSON(t, "/cards/acme.example/embed?csrf="+csrf, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("POST /cards/acme.example/embed = %d, ожидался 200: %s", resp.StatusCode, body)
	}
	defer resp.Body.Close()
	var payload struct {
		CompanyID     string `json:"company_id"`
		Dimensions    int    `json:"dimensions"`
		SourceVersion string `json:"source_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("разбор ответа эмбеддинга: %v", err)
	}
	if payload.CompanyID != "acme.example" || payload.Dimensions == 0 || payload.SourceVersion == "" {
		t.Fatalf("ответ эмбеддинга = %+v", payload)
	}
}
