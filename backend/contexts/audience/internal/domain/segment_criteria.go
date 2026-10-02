package domain

import (
	"fmt"
	"sort"
	"strings"
)

// SegmentCriteria — неизменяемое выражение условий включения лида в сегмент
// (VO segment-criteria). Сегмент отбирает лида, когда его оценка не ниже порога
// и наблюдаемые сигналы удовлетворяют перечню обязательных.
type SegmentCriteria struct {
	minScore        ScoreValue
	requiredSignals []string
	requireAll      bool
}

// NewSegmentCriteria собирает критерии из порога, перечня сигналов и режима
// сопоставления. Перечень очищается от пробелов, дублей и сортируется, поэтому
// выражение канонично и неизменяемо.
func NewSegmentCriteria(minScore ScoreValue, requiredSignals []string, requireAll bool) (SegmentCriteria, error) {
	normalized := normalizeSignals(requiredSignals)
	if requireAll && len(normalized) == 0 {
		return SegmentCriteria{}, ErrRequiredSignalsWithoutMatch
	}
	return SegmentCriteria{
		minScore:        minScore,
		requiredSignals: normalized,
		requireAll:      requireAll,
	}, nil
}

// MinScore возвращает порог оценки для включения в сегмент.
func (c SegmentCriteria) MinScore() ScoreValue { return c.minScore }

// RequiredSignals возвращает копию канонического перечня обязательных сигналов.
func (c SegmentCriteria) RequiredSignals() []string {
	return append([]string(nil), c.requiredSignals...)
}

// RequireAll сообщает, требуется ли совпадение всех сигналов (true) или
// достаточно любого из них (false).
func (c SegmentCriteria) RequireAll() bool { return c.requireAll }

// validate проверяет внутреннюю согласованность критериев при восстановлении.
func (c SegmentCriteria) validate() error {
	if c.minScore.value < 0 || c.minScore.value > 1 {
		return fmt.Errorf("%w: порог %s", ErrInvalidScore, c.minScore)
	}
	if c.requireAll && len(c.requiredSignals) == 0 {
		return ErrRequiredSignalsWithoutMatch
	}
	for _, name := range c.requiredSignals {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: пустое имя сигнала", ErrInvalidCriteria)
		}
	}
	return nil
}

// normalizeSignals приводит перечень сигналов к каноническому виду.
func normalizeSignals(signals []string) []string {
	seen := make(map[string]struct{}, len(signals))
	out := make([]string, 0, len(signals))
	for _, name := range signals {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}
