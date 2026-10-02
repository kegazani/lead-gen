package domain

import (
	"fmt"
	"strconv"
)

// ScoreValue — нормализованная оценка соответствия лида целевой аудитории
// (VO score-value). Значение лежит в диапазоне [0, 1] включительно; ноль —
// валидная оценка (нет подтверждённых сигналов), а не отсутствие оценки.
type ScoreValue struct {
	value float64
}

// NewScoreValue собирает оценку из числа, отвергая значения вне [0, 1] и NaN.
func NewScoreValue(value float64) (ScoreValue, error) {
	if value != value || value < 0 || value > 1 {
		return ScoreValue{}, fmt.Errorf("%w: %v", ErrInvalidScore, value)
	}
	return ScoreValue{value: value}, nil
}

// ZeroScore — оценка без подтверждённых сигналов.
func ZeroScore() ScoreValue { return ScoreValue{} }

// Float64 возвращает числовое значение оценки.
func (s ScoreValue) Float64() float64 { return s.value }

// compare возвращает -1, 0 или 1 при сравнении с другой оценкой.
func (s ScoreValue) compare(other ScoreValue) int {
	switch {
	case s.value < other.value:
		return -1
	case s.value > other.value:
		return 1
	default:
		return 0
	}
}

// AtLeast сообщает, что оценка не ниже порога.
func (s ScoreValue) AtLeast(threshold ScoreValue) bool { return s.value >= threshold.value }

// String возвращает оценку в виде строки без потери точности.
func (s ScoreValue) String() string { return strconv.FormatFloat(s.value, 'f', -1, 64) }
