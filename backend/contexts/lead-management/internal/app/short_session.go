package app

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
)

// Числовые значения короткой проверочной сессии по умолчанию (design.md,
// Решение 10). Это лишь начальные значения формы запуска: сам путь запуска
// (EnqueueShortSession) требует явных положительных значений и дефолты вместо
// отсутствующих величин не подставляет.
const (
	// DefaultProbeDepth — глубина обхода короткой сессии по умолчанию.
	DefaultProbeDepth = 1
	// DefaultProbeTimeSeconds — время работы короткой сессии по умолчанию, секунды.
	DefaultProbeTimeSeconds = 60
	// DefaultProbeMaxCredits — бюджет кредитов короткой сессии по умолчанию.
	DefaultProbeMaxCredits = 5
)

// DefaultProbeLimits возвращает лимиты обхода короткой сессии по умолчанию.
func DefaultProbeLimits() ports.PlanLimits {
	return ports.PlanLimits{MaxDepth: DefaultProbeDepth, MaxTimeSeconds: DefaultProbeTimeSeconds}
}

// EnqueueShortSession ставит короткую проверочную сессию: запуск поиска с ЯВНЫМИ
// ограничениями обхода (глубина и время) и бюджетом кредитов поверх
// RunSearch.EnqueueInterpreted — критерии релевантности выводятся тем же путём,
// что и у обычного запуска, а цели плана передаются явно.
//
// Явные ограничения обязательны: сессия не отправляется без положительных
// MaxDepth и MaxTimeSeconds — значения по умолчанию не подставляются. Бюджет тоже
// задаётся явно: неположительный maxCredits отклоняется. Применение бюджета и
// лимитов остаётся за EnqueueInterpreted и payload задания (MaxCredits): здесь оно не
// дублируется, повторная валидация запроса и бюджета делегируется EnqueueInterpreted.
//
// Отдельного признака «короткая сессия» в контракте не вводится: сессию отличают
// именно явные ограничения и бюджет, а распределение оценок даёт прогресс задания
// по вердиктам. Новое поле контракта ради пометки не добавляется. targetCompanies
// и maxResults прокидываются в общий путь постановки без интерпретации значений
// по умолчанию: ноль означает «цель не задана» и «лимит адаптера».
func (r *RunSearch) EnqueueShortSession(ctx context.Context, criteria string, planTargets []ports.PlanTarget, limits ports.PlanLimits, maxCredits, targetCompanies, maxResults int) (ports.SearchJob, error) {
	if limits.MaxDepth <= 0 || limits.MaxTimeSeconds <= 0 {
		return ports.SearchJob{}, fmt.Errorf(
			"app: короткая сессия требует явных ограничений обхода (глубина > 0 и время > 0): max_depth=%d, max_time_seconds=%d",
			limits.MaxDepth, limits.MaxTimeSeconds)
	}
	if maxCredits <= 0 {
		return ports.SearchJob{}, fmt.Errorf(
			"app: короткая сессия требует положительного бюджета кредитов: max_credits=%d", maxCredits)
	}
	if err := ports.ValidateTargetCompanies(targetCompanies); err != nil {
		return ports.SearchJob{}, err
	}
	if r.Logger != nil {
		r.Logger.Info("запуск короткой проверочной сессии", observ.Fields{},
			"max_depth", limits.MaxDepth, "max_time_seconds", limits.MaxTimeSeconds, "max_credits", maxCredits,
			"target_companies", targetCompanies, "max_results", maxResults)
	}
	return r.EnqueueInterpreted(ctx, criteria, planTargets, limits, maxCredits, targetCompanies, maxResults)
}
