// Публичная поверхность векторного порта контекста integration-platform.
//
// cmd/lead-gen не импортирует internal-пакеты напрямую, поэтому конструкторы
// адаптеров (реальный эмбеддер и персистентный индекс) публикуются корневым
// пакетом сборки сервиса — так же, как NewOpenAI, NewFirecrawl и другие.
package integrationplatform

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/openai"
	vectoradapter "lead-gen/backend/contexts/integration-platform/internal/adapters/vector"
	vectorpostgres "lead-gen/backend/contexts/integration-platform/internal/adapters/vector/postgres"
)

// VectorEmbedderConfig — конфигурация реального эмбеддера поверх
// OpenAI-совместимого endpoint эмбеддингов. Внутренние детали (HTTP-клиент,
// таймаут) настраиваются внутри адаптера и сюда не протекают.
type VectorEmbedderConfig struct {
	// BaseURL — базовый адрес API эмбеддингов. Пусто — адрес по умолчанию адаптера.
	BaseURL string
	// Model — модель эмбеддингов. Пусто — модель по умолчанию адаптера.
	Model string
	// APIKey — ключ доступа провайдера. Обязателен.
	APIKey string
}

// NewVectorEmbedder собирает реальный эмбеддер. Возвращает ошибку, если не задан
// ключ провайдера: детерминированный HashEmbedder остаётся тест/dev-двойником и
// на продуктовом пути не подставляется.
func NewVectorEmbedder(cfg VectorEmbedderConfig) (*openai.Embedder, error) {
	return openai.NewEmbedder(openai.EmbedderConfig{
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
	})
}

// VectorEmbedder — эмбеддер, которым пользуется векторный порт. Совпадает по
// методам с внутренним портом эмбеддера векторных адаптеров; публикуется, чтобы
// сигнатура конструкторов не протекала внутренним типом владельца.
type VectorEmbedder interface {
	// EmbedText строит вектор для непустого текста.
	EmbedText(ctx context.Context, text string) ([]float32, error)
}

// NewVectorPostgres собирает персистентный векторный индекс поверх пула зелёной
// зоны и реального эмбеддера. Возвращённое хранилище реализует
// contract/integration-platform/vector.Port; повторный upsert заменяет документ,
// а состояние переживает рестарт процесса.
func NewVectorPostgres(pool *pgxpool.Pool, embedder VectorEmbedder) *vectorpostgres.Store {
	return vectorpostgres.NewStore(pool, embedder)
}

// Проверка на этапе компиляции: публикуемый эмбеддер удовлетворяет и публичному,
// и внутреннему порту эмбеддера, которым пользуются векторные адаптеры.
var _ VectorEmbedder = (*openai.Embedder)(nil)
var _ vectoradapter.Embedder = (*openai.Embedder)(nil)
