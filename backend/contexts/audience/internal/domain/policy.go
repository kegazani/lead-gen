package domain

// Политика segment-eligibility: решение о включении лида в сегмент.
//
// Политика вынесена из агрегата в чистую функцию, потому что решение зависит
// только от критериев сегмента и оценки лида. Вход политики — контракт
// lead-profile (владелец lead-management): аудитория применяет присланные
// признаки, но не воспроизводит правила их получения.

// Eligible сообщает, удовлетворяет ли оценённый лид критериям сегмента.
//
// Условия: оценка не ниже порога, и — если задан перечень сигналов — выполнено
// требование по ним (все при requireAll, иначе хотя бы один). Наличие сигнала —
// это ненулевая сила: нулевой сигнал не считается подтверждённым.
func Eligible(criteria SegmentCriteria, score *LeadScore) bool {
	if score == nil {
		return false
	}
	if !score.value.AtLeast(criteria.minScore) {
		return false
	}
	if len(criteria.requiredSignals) == 0 {
		return true
	}

	present := make(map[string]struct{}, len(score.signals))
	for _, signal := range score.signals {
		if signal.strength > 0 {
			present[signal.name] = struct{}{}
		}
	}
	if criteria.requireAll {
		for _, name := range criteria.requiredSignals {
			if _, ok := present[name]; !ok {
				return false
			}
		}
		return true
	}
	for _, name := range criteria.requiredSignals {
		if _, ok := present[name]; ok {
			return true
		}
	}
	return false
}
