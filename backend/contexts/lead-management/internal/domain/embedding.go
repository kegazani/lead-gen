package domain

import (
	"fmt"
	"strings"
	"time"
)

// Embedding — векторное представление карточки компании (атрибут lead-profile).
//
// SourceVersion — watermark исходных данных: значение, из которого посчитан
// вектор. Если данные компании изменились и watermark не совпадает, эмбеддинг
// считается устаревшим и подлежит пересчёту (capability semantic-lead-search).
type Embedding struct {
	vector        []float32
	sourceVersion string
	computedAt    time.Time
}

// EmbeddingSnapshot — сохранённое состояние эмбеддинга.
type EmbeddingSnapshot struct {
	Vector        []float32
	SourceVersion string
	ComputedAt    time.Time
}

// NewEmbedding собирает эмбеддинг, проверяя, что вектор непуст, а watermark задан:
// эмбеддинг без источника актуальности нельзя пересчитать вовремя.
func NewEmbedding(vector []float32, sourceVersion string, at time.Time) (Embedding, error) {
	if len(vector) == 0 {
		return Embedding{}, fmt.Errorf("%w: пустой вектор", ErrInvalidEmbedding)
	}
	if strings.TrimSpace(sourceVersion) == "" {
		return Embedding{}, fmt.Errorf("%w: не задан watermark исходных данных", ErrInvalidEmbedding)
	}
	if at.IsZero() {
		return Embedding{}, fmt.Errorf("%w: не задано время расчёта", ErrInvalidEmbedding)
	}
	return Embedding{
		vector:        append([]float32(nil), vector...),
		sourceVersion: strings.TrimSpace(sourceVersion),
		computedAt:    at,
	}, nil
}

// IsZero сообщает, что эмбеддинг не сформирован.
func (e Embedding) IsZero() bool { return len(e.vector) == 0 }

// Vector возвращает копию вектора.
func (e Embedding) Vector() []float32 { return append([]float32(nil), e.vector...) }

// SourceVersion возвращает watermark исходных данных.
func (e Embedding) SourceVersion() string { return e.sourceVersion }

// ComputedAt возвращает время расчёта.
func (e Embedding) ComputedAt() time.Time { return e.computedAt }

// MatchesSource сообщает, что эмбеддинг посчитан из указанной версии данных.
func (e Embedding) MatchesSource(version string) bool {
	return !e.IsZero() && e.sourceVersion == strings.TrimSpace(version)
}

// snapshot возвращает состояние эмбеддинга для сохранения.
func (e Embedding) snapshot() EmbeddingSnapshot {
	return EmbeddingSnapshot{
		Vector:        append([]float32(nil), e.vector...),
		SourceVersion: e.sourceVersion,
		ComputedAt:    e.computedAt,
	}
}

// rehydrateEmbedding восстанавливает эмбеддинг из снимка. Нулевой снимок даёт
// нулевой эмбеддинг: отсутствие вектора — допустимое состояние.
func rehydrateEmbedding(s EmbeddingSnapshot) (Embedding, error) {
	if len(s.Vector) == 0 && strings.TrimSpace(s.SourceVersion) == "" && s.ComputedAt.IsZero() {
		return Embedding{}, nil
	}
	return NewEmbedding(s.Vector, s.SourceVersion, s.ComputedAt)
}
