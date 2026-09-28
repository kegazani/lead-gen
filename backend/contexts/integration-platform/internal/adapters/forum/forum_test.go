package forum

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/firecrawl"
	"lead-gen/contracts/integration-platform/contracttest"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/search"
)

// newSearchServer поднимает локальный httptest-сервер, отвечающий на поиск
// детерминированными результатами. Авторизация не проверяется.
func newSearchServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"data": []map[string]any{
				{"url": "https://reddit.com/r/saas/t/1", "title": "Боли SaaS-агентств", "description": "обсуждение найма"},
				{"url": "https://news.ycombinator.com/item?id=1", "title": "Ищем подрядчика", "description": "тема про аутсорс"},
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

// TestForumPortContract прогоняет contract suite порта forum против адаптера,
// оборачивающего поиск Firecrawl, который работает с локальным httptest-сервером.
func TestForumPortContract(t *testing.T) {
	srv := newSearchServer(t)
	defer srv.Close()
	searchPort := firecrawl.New(firecrawl.Config{BaseURL: srv.URL})

	contracttest.RunForumPortContract(t, func(t *testing.T) forum.Port {
		t.Helper()
		return New(Config{Search: searchPort})
	})
}

// recordingSearch — фейк поискового движка: фиксирует запросы и возвращает
// заготовленные результаты.
type recordingSearch struct {
	queries []search.Query
	results []search.Result
}

func (r *recordingSearch) Search(_ context.Context, q search.Query) ([]search.Result, error) {
	r.queries = append(r.queries, q)
	return r.results, nil
}

// TestSearchSignalsMapsAndRestricts проверяет, что адаптер ограничивает запрос
// доменами форумов и отображает результаты поиска на forum.Signal.
func TestSearchSignalsMapsAndRestricts(t *testing.T) {
	rs := &recordingSearch{
		results: []search.Result{{URL: "https://reddit.com/t/1", Title: "Тема", Snippet: "Выжимка"}},
	}
	c := New(Config{Search: rs, Domains: []string{"reddit.com"}})

	signals, err := c.SearchSignals(context.Background(), forum.Query{Text: "боли агентств"})
	if err != nil {
		t.Fatalf("SearchSignals: %v", err)
	}
	if len(signals) != 1 {
		t.Fatalf("сигналов %d, ожидался 1", len(signals))
	}
	s := signals[0]
	if s.Topic != "Тема" || s.Summary != "Выжимка" || s.SourceURL != "https://reddit.com/t/1" {
		t.Fatalf("неверное отображение результата: %+v", s)
	}

	if len(rs.queries) != 1 {
		t.Fatalf("запросов к поиску %d, ожидался 1", len(rs.queries))
	}
	if rs.queries[0].Text != "боли агентств site:reddit.com" {
		t.Fatalf("запрос не ограничен доменом: %q", rs.queries[0].Text)
	}
}

// TestRestrict проверяет, что пустой список доменов оставляет запрос без
// ограничения, а непустой — добавляет операторы site:.
func TestRestrict(t *testing.T) {
	c := New(Config{Domains: []string{"reddit.com", "news.ycombinator.com"}})
	if got, want := c.restrict("боли агентств"), "боли агентств site:reddit.com OR site:news.ycombinator.com"; got != want {
		t.Fatalf("restrict = %q, ожидалось %q", got, want)
	}

	empty := &Client{domains: nil}
	if got, want := empty.restrict("боли агентств"), "боли агентств"; got != want {
		t.Fatalf("restrict с пустыми доменами = %q, ожидалось %q", got, want)
	}
}
