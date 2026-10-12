package firecrawl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"lead-gen/contracts/integration-platform/documentparse"
)

// scrapeBodyProbe — тело запроса /v1/scrape для проверки параметров разбора.
type scrapeBodyProbe struct {
	URL     string   `json:"url"`
	Formats []string `json:"formats"`
	Parsers []string `json:"parsers"`
}

// TestParseDocumentRequestsMarkdown проверяет, что разбор документа — реальный
// HTTP-вызов /v1/scrape: адаптер отправляет режим markdown, передаёт подсказку
// формата полем parsers и возвращает извлечённый текст, а не фиксированный ответ.
func TestParseDocumentRequestsMarkdown(t *testing.T) {
	const docURL = "https://client.example/kp.pdf"

	type capture struct {
		calls int
		body  scrapeBodyProbe
	}
	var (
		mu  sync.Mutex
		got capture
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/scrape", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var probe scrapeBodyProbe
		_ = json.Unmarshal(body, &probe)
		mu.Lock()
		got.calls++
		got.body = probe
		mu.Unlock()
		writeJSON(t, w, map[string]any{
			"success": true,
			"data": map[string]any{
				"url":      docURL,
				"markdown": "# Коммерческое предложение\n\nСроки и стоимость по запросу.",
				"metadata": map[string]any{"title": "КП"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	document, err := client.ParseDocument(context.Background(), documentparse.Request{
		Source: docURL,
		Format: "pdf",
	})
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}

	mu.Lock()
	captured := got
	mu.Unlock()
	if captured.calls != 1 {
		t.Fatalf("ожидался один HTTP-вызов, получено %d", captured.calls)
	}
	if captured.body.URL != docURL {
		t.Fatalf("url = %q, ожидался %q", captured.body.URL, docURL)
	}
	if !reflect.DeepEqual(captured.body.Formats, []string{"markdown"}) {
		t.Fatalf("formats = %v, ожидался [markdown]", captured.body.Formats)
	}
	if !reflect.DeepEqual(captured.body.Parsers, []string{"pdf"}) {
		t.Fatalf("parsers = %v, ожидался [pdf]", captured.body.Parsers)
	}
	if document.Source != docURL {
		t.Fatalf("source = %q, ожидался %q", document.Source, docURL)
	}
	if document.Format != "pdf" {
		t.Fatalf("format = %q, ожидался pdf", document.Format)
	}
	if !strings.HasPrefix(document.Text, "# Коммерческое предложение") {
		t.Fatalf("текст документа не извлечён: %q", document.Text)
	}
}

// TestParseDocumentOmitsParsersWithoutFormat проверяет, что без подсказки формата
// поле parsers не отправляется: формат определяет движок.
func TestParseDocumentOmitsParsersWithoutFormat(t *testing.T) {
	var (
		mu   sync.Mutex
		body scrapeBodyProbe
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/scrape", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var probe scrapeBodyProbe
		_ = json.Unmarshal(raw, &probe)
		mu.Lock()
		body = probe
		mu.Unlock()
		writeJSON(t, w, map[string]any{
			"success": true,
			"data":    map[string]any{"url": "https://client.example/kp", "markdown": "текст"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	if _, err := client.ParseDocument(context.Background(), documentparse.Request{Source: "https://client.example/kp"}); err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if body.Parsers != nil {
		t.Fatalf("parsers не должен отправляться без формата: %v", body.Parsers)
	}
	if !reflect.DeepEqual(body.Formats, []string{"markdown"}) {
		t.Fatalf("formats = %v, ожидался [markdown]", body.Formats)
	}
}

// TestParseDocumentRejectsEmptySource проверяет, что пустой источник не доходит до
// HTTP: контракт отвергает его раньше.
func TestParseDocumentRejectsEmptySource(t *testing.T) {
	client := New(Config{BaseURL: "http://127.0.0.1:0"})
	if _, err := client.ParseDocument(context.Background(), documentparse.Request{}); err == nil {
		t.Fatal("пустой источник должен быть отвергнут")
	}
}
