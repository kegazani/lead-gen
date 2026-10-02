// Package vector реализует векторное хранилище за provider-port: эмбеддинг текста
// и поиск по близости.
//
// Это внешняя зависимость уровня provider (как Firecrawl или LLM), а не доменный
// слой: пакет не знает ни о компаниях, ни о вердиктах релевантности. Ошибки
// низкоуровневого эмбеддера нормализуются до ошибок контракта
// (contract.ErrUnavailable/ErrRejected), поэтому замена хранилища не меняет
// наблюдаемое поведение потребителя.
package vector

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"

	contract "lead-gen/contracts/integration-platform/vector"
)

const (
	// defaultDimension — размерность детерминированного эмбеддинга.
	defaultDimension = 64
	// defaultLimit — число ближайших соседей по умолчанию.
	defaultLimit = 10
)

// Embedder — низкоуровневый генератор эмбеддинга (внешний сервис). Ошибку
// эмбеддера адаптер нормализует до contract.ErrUnavailable.
type Embedder interface {
	EmbedText(ctx context.Context, text string) ([]float32, error)
}

// Memory — реализация contract.Port для dev и тестов: детерминированный
// эмбеддер и индекс ближайших соседей по косинусной близости. Продовая реализация
// даёт ту же форму поверх внешнего хранилища (pgvector или отдельный индекс).
type Memory struct {
	embedder  Embedder
	mu        sync.RWMutex
	documents map[string]contract.Vector
}

// Memory реализует контракт векторного хранилища.
var _ contract.Port = (*Memory)(nil)

// NewMemory собирает хранилище поверх эмбеддера. nil-эмбеддер заменяется
// детерминированным HashEmbedder.
func NewMemory(embedder Embedder) *Memory {
	if embedder == nil {
		embedder = HashEmbedder{Dimension: defaultDimension}
	}
	return &Memory{embedder: embedder, documents: make(map[string]contract.Vector)}
}

// Embed реализует contract.Port.
func (m *Memory) Embed(ctx context.Context, text string) (contract.Vector, error) {
	return m.embed(ctx, text)
}

// Upsert реализует contract.Port: документ индексируется, его вектор сохраняется.
func (m *Memory) Upsert(ctx context.Context, document contract.Document) (contract.Vector, error) {
	if strings.TrimSpace(document.ID) == "" {
		return nil, fmt.Errorf("%w: пустой идентификатор документа", contract.ErrRejected)
	}
	vec, err := m.embed(ctx, document.Text)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.documents[document.ID] = vec
	m.mu.Unlock()
	return vec, nil
}

// Search реализует contract.Port: ближайшие документы по убыванию близости.
func (m *Memory) Search(ctx context.Context, text string, k int) ([]contract.Match, error) {
	query, err := m.embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if k <= 0 {
		k = defaultLimit
	}
	m.mu.RLock()
	matches := make([]contract.Match, 0, len(m.documents))
	for id, vec := range m.documents {
		matches = append(matches, contract.Match{ID: id, Score: cosine(query, vec)})
	}
	m.mu.RUnlock()

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > k {
		matches = matches[:k]
	}
	return matches, nil
}

// embed строит нормализованный вектор, нормализуя ошибки эмбеддера к контракту.
func (m *Memory) embed(ctx context.Context, text string) (contract.Vector, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: пустой текст", contract.ErrRejected)
	}
	raw, err := m.embedder.EmbedText(ctx, trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", contract.ErrUnavailable, err)
	}
	vec := normalize(raw)
	if len(vec) == 0 {
		return nil, fmt.Errorf("%w: эмбеддер вернул пустой вектор", contract.ErrUnavailable)
	}
	return vec, nil
}

// HashEmbedder — детерминированный эмбеддер по словам: подходит для dev и тестов,
// где важна воспроизводимость, а не качество семантики. Совпадающие слова дают
// близкие векторы.
type HashEmbedder struct {
	// Dimension — размерность вектора; 0 означает defaultDimension.
	Dimension int
}

// HashEmbedder реализует Embedder.
var _ Embedder = HashEmbedder{}

// EmbedText реализует Embedder.
func (h HashEmbedder) EmbedText(_ context.Context, text string) ([]float32, error) {
	dim := h.Dimension
	if dim <= 0 {
		dim = defaultDimension
	}
	vec := make([]float32, dim)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = strings.Trim(token, ".,;:!?()[]{}\"'«»—")
		if token == "" {
			continue
		}
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(token))
		vec[int(hash.Sum32()%uint32(dim))]++
	}
	return vec, nil
}

// normalize приводит вектор к единичной L2-норме, чтобы скалярное произведение
// равнялось косинусной близости.
func normalize(raw []float32) contract.Vector {
	var sumSquares float64
	for _, v := range raw {
		sumSquares += float64(v) * float64(v)
	}
	if sumSquares == 0 {
		return nil
	}
	norm := math.Sqrt(sumSquares)
	out := make(contract.Vector, len(raw))
	for i, v := range raw {
		out[i] = float32(float64(v) / norm)
	}
	return out
}

// cosine возвращает косинусную близость двух нормализованных векторов.
func cosine(a, b contract.Vector) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}
