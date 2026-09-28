package firecrawl

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/crawl"
)

// TestCrawlLogExcludesAPIKey проверяет приёмку «наблюдаемость без секретов»: лог
// параметров обхода не содержит ключа авторизации и несёт безопасные параметры
// запроса.
func TestCrawlLogExcludesAPIKey(t *testing.T) {
	const apiKey = "SECRET-FIRECRAWL-KEY"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"success": true, "data": []map[string]any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var buf bytes.Buffer
	client := New(Config{BaseURL: srv.URL, APIKey: apiKey, Logger: observ.NewLoggerWithWriter(&buf)})
	if _, err := client.Crawl(context.Background(), crawl.Request{
		StartURL: "https://www.kp.ru/russia/moskva/nedvizhimost/luchshie-zastrojshhiki/",
		MaxPages: 10,
		MaxDepth: 1,
	}); err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	log := buf.String()
	if strings.Contains(log, apiKey) {
		t.Fatalf("лог содержит ключ авторизации:\n%s", log)
	}
	for _, want := range []string{"url", "limit", "maxDiscoveryDepth", "maxDepth"} {
		if !strings.Contains(log, want) {
			t.Fatalf("лог не содержит поле %q:\n%s", want, log)
		}
	}
}
