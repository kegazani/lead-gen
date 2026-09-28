package firecrawl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"lead-gen/contracts/integration-platform/contracttest"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

// newTestServer поднимает локальный httptest-сервер, отвечающий на ручки v1 API
// Firecrawl детерминированными данными. Он не проверяет авторизацию, поэтому
// клиент собирается с пустым ключом.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"data": []map[string]any{
				{"url": "https://example.com/a", "title": "A", "description": "сниппет a"},
				{"url": "https://example.com/b", "title": "B", "description": "сниппет b"},
			},
		})
	})

	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"data": []map[string]any{
				{"url": "https://example.com", "markdown": "# страница", "metadata": map[string]any{"title": "Главная"}},
			},
		})
	})

	mux.HandleFunc("/v1/scrape", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"data": map[string]any{
				// Структурированный результат Firecrawl v1 приходит под data.json.
				"json": map[string]any{
					"contacts": []map[string]any{
						{"name": "Иван", "email": "ivan@example.com", "phone": "+7 900 000-00-00", "role": "CEO"},
					},
				},
			},
		})
	})

	return httptest.NewServer(mux)
}

// writeJSON сериализует ответ и падает тест при ошибке.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("запись ответа: %v", err)
	}
}

// TestSearchPortContract прогоняет contract suite порта search против адаптера
// Firecrawl, работающего с локальным httptest-сервером.
func TestSearchPortContract(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	contracttest.RunSearchPortContract(t, func(t *testing.T) search.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestCrawlPortContract прогоняет contract suite порта crawl против адаптера
// Firecrawl, работающего с локальным httptest-сервером.
func TestCrawlPortContract(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	contracttest.RunCrawlPortContract(t, func(t *testing.T) crawl.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestCrawlAsyncPollsUntilComplete проверяет несинхронный путь: /v1/crawl
// возвращает id задания, статус опрашивается до completed, и страницы отображаются
// на crawl.Page.
func TestCrawlAsyncPollsUntilComplete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"success": true, "id": "job-1"})
	})
	mux.HandleFunc("/v1/crawl/job-1", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"status":  "completed",
			"data": []map[string]any{
				{"url": "https://example.com/deep", "markdown": "# глубина"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	outcome, err := client.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if len(outcome.Pages) != 1 || outcome.Pages[0].URL != "https://example.com/deep" {
		t.Fatalf("неожиданный исход: %+v", outcome.Pages)
	}
}

// TestExtractRequestsJSONFormat проверяет, что структурированная экстракция
// контактов запрашивается корректным для Firecrawl v1 способом: форматом «json»
// с jsonOptions (схема + systemPrompt), а не несуществующим «extract», и что
// контакты читаются из data.json.
func TestExtractRequestsJSONFormat(t *testing.T) {
	type scrapeBody struct {
		URL         string   `json:"url"`
		Formats     []string `json:"formats"`
		JSONOptions struct {
			SystemPrompt string         `json:"systemPrompt"`
			Schema       map[string]any `json:"schema"`
		} `json:"jsonOptions"`
		Extract map[string]any `json:"extract"`
	}

	var captured scrapeBody
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/scrape", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		writeJSON(t, w, map[string]any{
			"success": true,
			"data": map[string]any{
				"json": map[string]any{
					"contacts": []map[string]any{{"email": "found@example.com"}},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	contacts, err := client.Extract(context.Background(), crawl.ExtractRequest{URL: "https://example.com/contacts"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !reflect.DeepEqual(captured.Formats, []string{"json"}) {
		t.Fatalf("formats = %v, ожидался [json]", captured.Formats)
	}
	if captured.Extract != nil {
		t.Fatalf("несуществующий параметр extract не должен отправляться: %v", captured.Extract)
	}
	if captured.JSONOptions.SystemPrompt == "" {
		t.Fatalf("jsonOptions.systemPrompt пуст")
	}
	if captured.JSONOptions.Schema == nil {
		t.Fatalf("jsonOptions.schema пуст")
	}
	if len(contacts) != 1 || contacts[0].Email != "found@example.com" {
		t.Fatalf("контакты = %+v, ожидался один с found@example.com", contacts)
	}
}

// TestCrawlUsesDiscoveryDepthNotMaxDepth проверяет исправление семантики глубины:
// параметр глубины пользователя (число переходов по ссылкам) уходит как
// maxDiscoveryDepth, а maxDepth (абсолютная глубина пути URL) не отправляется,
// поэтому вложенный стартовый URL с глубиной 0 и 1 принимается без HTTP 400.
func TestCrawlUsesDiscoveryDepthNotMaxDepth(t *testing.T) {
	const startURL = "https://www.kp.ru/russia/moskva/nedvizhimost/luchshie-zastrojshhiki/"

	type crawlBody struct {
		URL               string `json:"url"`
		Limit             int    `json:"limit"`
		MaxDepth          *int   `json:"maxDepth"`
		MaxDiscoveryDepth *int   `json:"maxDiscoveryDepth"`
		IgnoreSitemap     *bool  `json:"ignoreSitemap"`
	}

	var (
		mu       sync.Mutex
		captured crawlBody
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, r *http.Request) {
		var b crawlBody
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		captured = b
		mu.Unlock()
		writeJSON(t, w, map[string]any{"success": true, "data": []map[string]any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	for _, depth := range []int{0, 1} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			if _, err := client.Crawl(context.Background(), crawl.Request{
				StartURL: startURL,
				MaxPages: 10,
				MaxDepth: depth,
			}); err != nil {
				t.Fatalf("Crawl: %v", err)
			}

			mu.Lock()
			b := captured
			mu.Unlock()

			if b.URL != startURL {
				t.Fatalf("url = %q, want %q", b.URL, startURL)
			}
			if b.Limit != 10 {
				t.Fatalf("limit = %d, want 10", b.Limit)
			}
			if b.MaxDepth != nil {
				t.Fatalf("maxDepth не должен отправляться, получено %d", *b.MaxDepth)
			}
			if b.MaxDiscoveryDepth == nil || *b.MaxDiscoveryDepth != depth {
				t.Fatalf("maxDiscoveryDepth = %v, want %d", b.MaxDiscoveryDepth, depth)
			}
			if b.IgnoreSitemap == nil || !*b.IgnoreSitemap {
				t.Fatalf("ignoreSitemap = %v, want true", b.IgnoreSitemap)
			}
		})
	}
}

// TestCrawlOmitsLimitWhenMaxPagesZero проверяет, что при нулевом MaxPages тело
// запроса к Firecrawl не содержит поле limit: движок обходит в своём режиме, а
// границей расхода заведует вызывающий (обёртка бюджета).
func TestCrawlOmitsLimitWhenMaxPagesZero(t *testing.T) {
	type crawlBody struct {
		URL   string `json:"url"`
		Limit *int   `json:"limit"`
	}

	var (
		mu       sync.Mutex
		captured crawlBody
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, r *http.Request) {
		var b crawlBody
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		captured = b
		mu.Unlock()
		writeJSON(t, w, map[string]any{"success": true, "data": []map[string]any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	if _, err := client.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com"}); err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	mu.Lock()
	b := captured
	mu.Unlock()
	if b.Limit != nil {
		t.Fatalf("при нулевом MaxPages поле limit не должно отправляться, получено %d", *b.Limit)
	}
}

// TestCrawlPageURLFromMetadata проверяет, что при отсутствии верхнеуровневого url
// адрес страницы берётся из metadata.sourceURL: без него обход теряет домен
// страницы и карточка компании не собирается.
func TestCrawlPageURLFromMetadata(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"status":  "completed",
			"data": []map[string]any{
				{
					"markdown": "# Компания",
					"metadata": map[string]any{
						"sourceURL": "https://acme.example/about",
						"title":     "О компании",
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	outcome, err := client.Crawl(context.Background(), crawl.Request{StartURL: "https://acme.example/"})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if len(outcome.Pages) != 1 {
		t.Fatalf("страниц = %d, ожидалась 1", len(outcome.Pages))
	}
	if got := outcome.Pages[0].URL; got != "https://acme.example/about" {
		t.Fatalf("URL страницы = %q, ожидался источник из metadata.sourceURL", got)
	}
	if got := outcome.Pages[0].Metadata["title"]; got != "О компании" {
		t.Fatalf("metadata.title = %q", got)
	}
}

// TestCrawlIncludePathsAlwaysCoverStartPage проверяет, что при непустых
// include-путях в запрос добавляется паттерн пути стартовой страницы. Без него
// Firecrawl отсекает и стартовую страницу, возвращая ноль страниц.
func TestCrawlIncludePathsAlwaysCoverStartPage(t *testing.T) {
	type crawlBody struct {
		URL          string   `json:"url"`
		IncludePaths []string `json:"includePaths"`
	}

	var (
		mu       sync.Mutex
		captured crawlBody
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, r *http.Request) {
		var b crawlBody
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		captured = b
		mu.Unlock()
		writeJSON(t, w, map[string]any{"success": true, "data": []map[string]any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := New(Config{BaseURL: srv.URL})

	const startURL = "https://goodstone.ru/msk/in-systems/"
	if _, err := client.Crawl(context.Background(), crawl.Request{
		StartURL:     startURL,
		MaxPages:     5,
		IncludePaths: []string{"contact", "about"},
	}); err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	mu.Lock()
	got := captured.IncludePaths
	mu.Unlock()

	wantStart := `^/msk/in-systems/?$`
	found := false
	for _, p := range got {
		if p == wantStart {
			found = true
		}
	}
	if !found {
		t.Fatalf("includePaths = %v, ожидался паттерн стартовой страницы %q", got, wantStart)
	}
}

// TestWithStartPagePath проверяет построение паттерна стартовой страницы,
// включая корень сайта и завершающий слэш.
func TestWithStartPagePath(t *testing.T) {
	cases := []struct {
		name     string
		startURL string
		wantLast string
	}{
		{name: "корень", startURL: "https://example.com/", wantLast: `^/?$`},
		{name: "корень без слэша", startURL: "https://example.com", wantLast: `^/?$`},
		{name: "вложенный путь", startURL: "https://example.com/a/b/", wantLast: `^/a/b/?$`},
		{name: "экранирование", startURL: "https://example.com/a.b", wantLast: `^/a\.b/?$`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withStartPagePath([]string{"contact"}, tc.startURL)
			if len(got) != 2 || got[1] != tc.wantLast {
				t.Fatalf("withStartPagePath = %v, ожидался последний паттерн %q", got, tc.wantLast)
			}
		})
	}
}

// TestValidateMaxDepthDetectsMismatch проверяет, что ошибочная комбинация URL и
// maxDepth (абсолютная глубина пути) выявляется локально, до отправки в Firecrawl.
func TestValidateMaxDepthDetectsMismatch(t *testing.T) {
	const startURL = "https://www.kp.ru/russia/moskva/nedvizhimost/luchshie-zastrojshhiki/"
	cases := []struct {
		name     string
		url      string
		maxDepth int
		wantErr  bool
	}{
		{name: "в пределах", url: startURL, maxDepth: 5},
		{name: "на границе", url: startURL, maxDepth: 4},
		{name: "превышает", url: startURL, maxDepth: 2, wantErr: true},
		{name: "корень с нулевой глубиной", url: "https://example.com/", maxDepth: 0},
		{name: "отрицательный maxDepth", url: startURL, maxDepth: -1, wantErr: true},
		{name: "неабсолютный URL", url: "/a/b", maxDepth: 5, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMaxDepth(tc.url, tc.maxDepth)
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для url=%q maxDepth=%d", tc.url, tc.maxDepth)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

// TestCrawlCancellationReturnsPartialPages проверяет приёмку «отмена с сохранением
// результата»: при отмене контекста обход возвращает crawl.ErrCancelled вместе с
// уже собранными страницами.
func TestCrawlCancellationReturnsPartialPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"success": true, "id": "job-1"})
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux.HandleFunc("/v1/crawl/job-1", func(w http.ResponseWriter, _ *http.Request) {
		// Первый опрос отменяет контекст: адаптер уйдёт на best-effort снимок.
		cancel()
		writeJSON(t, w, map[string]any{
			"success": true,
			"status":  "scraping",
			"data":    []map[string]any{{"url": "https://example.com/a", "markdown": "# A"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	outcome, err := client.Crawl(ctx, crawl.Request{StartURL: "https://example.com", MaxPages: 5})
	if err != crawl.ErrCancelled {
		t.Fatalf("Crawl = %v, ожидалась crawl.ErrCancelled", err)
	}
	if len(outcome.Pages) != 1 || outcome.Pages[0].URL != "https://example.com/a" {
		t.Fatalf("частичные страницы не сохранены: %+v", outcome.Pages)
	}
}

// TestCrawlCancellationStopsRemoteJob проверяет, что при отмене адаптер отправляет
// движку запрос остановки удалённого обхода (DELETE /v1/crawl/{id}), чтобы
// освободить ресурсы Firecrawl.
func TestCrawlCancellationStopsRemoteJob(t *testing.T) {
	var mu sync.Mutex
	deleteCalled := false

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"success": true, "id": "job-1"})
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux.HandleFunc("/v1/crawl/job-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			deleteCalled = true
			mu.Unlock()
			writeJSON(t, w, map[string]any{"success": true})
			return
		}
		// Опрос отменяет контекст: адаптер остановит удалённое задание.
		cancel()
		writeJSON(t, w, map[string]any{
			"success": true,
			"status":  "scraping",
			"data":    []map[string]any{{"url": "https://example.com/a", "markdown": "# A"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	if _, err := client.Crawl(ctx, crawl.Request{StartURL: "https://example.com", MaxPages: 5}); err != crawl.ErrCancelled {
		t.Fatalf("Crawl = %v, ожидалась crawl.ErrCancelled", err)
	}

	mu.Lock()
	got := deleteCalled
	mu.Unlock()
	if !got {
		t.Fatal("при отмене адаптер должен отправить DELETE /v1/crawl/{id} для остановки удалённого обхода")
	}
}
