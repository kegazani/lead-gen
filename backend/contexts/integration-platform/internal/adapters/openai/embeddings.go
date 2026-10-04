// Эмбеддер-адаптер поверх OpenAI-совместимого endpoint эмбеддингов
// (/v1/embeddings). Он переиспользует HTTP-клиент openai-адаптера (doJSON,
// endpoint), поэтому не дублирует транспорт и разбор ошибок: отдельный SDK не
// подключается, запрос — обычный POST с JSON.
//
// Эмбеддер не знает о контракте vector и не нормализует ошибки домена: он
// возвращает обычную ошибку Go, а нормализацию до contract.ErrUnavailable
// делает векторный порт (vector.Memory / vector/postgres).
package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// defaultEmbeddingModel — модель эмбеддингов по умолчанию для
// OpenAI-совместимого endpoint.
const defaultEmbeddingModel = "text-embedding-3-small"

// EmbedderConfig — настройки эмбеддера.
type EmbedderConfig struct {
	// BaseURL — базовый адрес API. Пустое значение заменяется на defaultBaseURL.
	BaseURL string
	// Model — идентификатор модели эмбеддингов. Пустое значение заменяется на
	// defaultEmbeddingModel.
	Model string
	// APIKey — ключ доступа. Обязателен: без него эмбеддер не собирается, чтобы
	// продуктовый путь не подменялся детерминированным двойником молча.
	APIKey string
	// HTTPClient — клиент для исходящих запросов. nil означает клиент адаптера с
	// конечным таймаутом (defaultHTTPTimeout).
	HTTPClient *http.Client
}

// Embedder строит векторы текста через OpenAI-совместимый endpoint эмбеддингов.
type Embedder struct {
	client *Client
	model  string
}

// NewEmbedder собирает эмбеддер поверх общего HTTP-клиента openai-адаптера.
// Пустой ключ доступа — ошибка сборки: без реального провайдера эмбеддинг не
// выполняется, а не заменяется детерминированным хешем (AGENTS.md §3).
func NewEmbedder(cfg EmbedderConfig) (*Embedder, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("openai: ключ провайдера эмбеддингов обязателен")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultEmbeddingModel
	}
	return &Embedder{
		client: New(Config{
			BaseURL:    cfg.BaseURL,
			Model:      model,
			APIKey:     apiKey,
			HTTPClient: cfg.HTTPClient,
		}),
		model: model,
	}, nil
}

// EmbedText строит вектор для непустого текста. Отмена и дедлайн контекста
// пробрасываются как есть; пустой ответ провайдера — ошибка, а не пустой вектор.
func (e *Embedder) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("openai: пустой текст для эмбеддинга")
	}

	var resp embeddingResponse
	body := embeddingRequest{Model: e.model, Input: trimmed}
	if err := e.client.doJSON(ctx, http.MethodPost, e.client.endpoint("/v1/embeddings"), body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai: провайдер вернул пустой эмбеддинг")
	}
	return resp.Data[0].Embedding, nil
}

// embeddingRequest — тело запроса эмбеддинга OpenAI-совместимого API.
type embeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// embeddingResponse — ответ провайдера: список эмбеддингов по позициям входа.
type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

// embeddingData — один вектор ответа.
type embeddingData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}
