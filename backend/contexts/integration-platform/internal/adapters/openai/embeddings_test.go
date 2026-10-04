package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmbedderEmbedsText проверяет, что эмбеддер обращается к endpoint
// /v1/embeddings, передаёт модель и текст и возвращает вектор провайдера.
func TestEmbedderEmbedsText(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotInput string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("разбор запроса: %v", err)
		}
		gotModel, gotInput = req.Model, req.Input
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": []float32{0.1, 0.2, 0.3}}},
		})
	}))
	defer srv.Close()

	embedder, err := NewEmbedder(EmbedderConfig{BaseURL: srv.URL, APIKey: "test-key", Model: "text-embedding-3-small"})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	vec, err := embedder.EmbedText(context.Background(), "  семантический текст  ")
	if err != nil {
		t.Fatalf("EmbedText: %v", err)
	}
	if gotPath != "/v1/embeddings" {
		t.Fatalf("путь запроса %q, ожидался /v1/embeddings", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, ожидался Bearer test-key", gotAuth)
	}
	if gotModel != "text-embedding-3-small" {
		t.Fatalf("модель %q, ожидалась text-embedding-3-small", gotModel)
	}
	if gotInput != "семантический текст" {
		t.Fatalf("текст %q, ожидался обрезанный", gotInput)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[2] != 0.3 {
		t.Fatalf("вектор %v, ожидался [0.1 0.2 0.3]", vec)
	}
}

// TestEmbedderRequiresAPIKey проверяет отсутствие молчаливой подмены: без ключа
// провайдера эмбеддер не собирается, а не откатывается на детерминированный
// хеш-эмбеддер продуктового пути.
func TestEmbedderRequiresAPIKey(t *testing.T) {
	if _, err := NewEmbedder(EmbedderConfig{BaseURL: "http://example.invalid"}); err == nil {
		t.Fatal("ожидалась ошибка сборки без ключа провайдера")
	}
}

// TestEmbedderRejectsEmptyText проверяет доменный отказ на пустом тексте.
func TestEmbedderRejectsEmptyText(t *testing.T) {
	embedder, err := NewEmbedder(EmbedderConfig{BaseURL: "http://example.invalid", APIKey: "k"})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	if _, err := embedder.EmbedText(context.Background(), "   "); err == nil {
		t.Fatal("ожидалась ошибка на пустом тексте")
	}
}

// TestEmbedderProviderFailure проверяет, что отказ провайдера возвращается
// обычной ошибкой Go, без утечки типа HTTP-ответа.
func TestEmbedderProviderFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "перегружен", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	embedder, err := NewEmbedder(EmbedderConfig{BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	if _, err := embedder.EmbedText(context.Background(), "текст"); err == nil {
		t.Fatal("ожидалась ошибка провайдера")
	}
}
