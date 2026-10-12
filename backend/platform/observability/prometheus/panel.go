package prometheus

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Panel — объявленная панель метрик: машинный ключ, подпись, единица измерения,
// PromQL-запрос, диапазон и шаг по умолчанию и необязательный токен подстановки.
// Запрос — свойство объявленной конфигурации, а не входных данных клиента: экран
// принимает только ключ панели, поэтому произвольный PromQL недоступен.
type Panel struct {
	// Key — машинный ключ панели (значение параметра запроса).
	Key string
	// Title — подпись панели (необязательна; пусто — ключ).
	Title string
	// Unit — единица измерения для подписи значения.
	Unit string
	// Query — PromQL-шаблон с необязательным токеном Token.
	Query string
	// Token — заполнитель в Query, подставляемый значением (например, "{{node}}").
	Token string
	// Range — диапазон по умолчанию.
	Range time.Duration
	// Step — шаг по умолчанию.
	Step time.Duration
}

// PanelSample — точка временного ряда панели.
type PanelSample struct {
	Time  time.Time
	Value float64
}

// PanelSeries — серия точек с метками.
type PanelSeries struct {
	Labels map[string]string
	Points []PanelSample
}

// Причины недоступности панели: машинные коды, а не пользовательский текст.
const (
	// ReasonSourceUnavailable — источник (Prometheus) недоступен или отказал.
	ReasonSourceUnavailable = "source_unavailable"
	// WarningStepCapped — шаг увеличен из-за предела точек на ответ.
	WarningStepCapped = "step_capped"
)

// PanelResult — исход чтения панели. Available отделяет «измерение есть» от
// честного отказа: недоступный источник даёт Available=false с Reason и без
// нулевых точек (нулевая серия — ложное измерение).
type PanelResult struct {
	Panel     Panel
	Available bool
	Range     time.Duration
	Step      time.Duration
	Series    []PanelSeries
	Warnings  []string
	// Reason — машинный код недоступности. Пустой при Available=true.
	Reason string
}

// Limits — пределы прокси метрик: число точек на ответ, максимальный диапазон и
// минимальный шаг. Предсказуемый отказ вместо перегрузки источника.
type Limits struct {
	MaxPoints int
	MaxRange  time.Duration
	MinStep   time.Duration
}

// DefaultLimits — пределы прокси по умолчанию.
func DefaultLimits() Limits {
	return Limits{MaxPoints: 300, MaxRange: 24 * time.Hour, MinStep: 15 * time.Second}
}

// withDefaults подставляет значения по умолчанию для нулевых полей.
func (l Limits) withDefaults() Limits {
	if l.MaxPoints <= 0 {
		l.MaxPoints = 300
	}
	if l.MaxRange <= 0 {
		l.MaxRange = 24 * time.Hour
	}
	if l.MinStep <= 0 {
		l.MinStep = 15 * time.Second
	}
	return l
}

// QueryRanger — часть клиента Prometheus, нужная прокси панелей.
type QueryRanger interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (Matrix, []string, error)
}

// ResolveQuery подставляет значение в токен запроса панели, экранируя его для
// строкового литерала PromQL. Пустой токен или значение оставляют запрос как есть.
func ResolveQuery(query, token, value string) string {
	token = strings.TrimSpace(token)
	if token == "" || value == "" {
		return query
	}
	return strings.ReplaceAll(query, token, labelValue(value))
}

// ResolveWindow проверяет запрошенные диапазон и шаг панели, ограничивает их
// пределами и возвращает пометки о применённых ограничениях. Некорректный
// параметр — ошибка вызывающему (это отказ, а не молчаливая подмена).
func ResolveWindow(panel Panel, rangeParam, stepParam string, limits Limits) (time.Duration, time.Duration, []string, error) {
	limits = limits.withDefaults()
	window := panel.Range
	if window <= 0 {
		window = time.Hour
	}
	if rangeParam != "" {
		parsed, err := time.ParseDuration(rangeParam)
		if err != nil || parsed <= 0 {
			return 0, 0, nil, fmt.Errorf("prometheus: некорректный диапазон %q", rangeParam)
		}
		if parsed > limits.MaxRange {
			parsed = limits.MaxRange
		}
		window = parsed
	}

	step := panel.Step
	if step <= 0 {
		step = time.Minute
	}
	if stepParam != "" {
		parsed, err := time.ParseDuration(stepParam)
		if err != nil || parsed <= 0 {
			return 0, 0, nil, fmt.Errorf("prometheus: некорректный шаг %q", stepParam)
		}
		step = parsed
	}
	if step < limits.MinStep {
		step = limits.MinStep
	}

	var warnings []string
	if points := int(window/step) + 1; points > limits.MaxPoints {
		step = time.Duration(int64(window) / int64(limits.MaxPoints))
		if step < limits.MinStep {
			step = limits.MinStep
		}
		warnings = append(warnings, WarningStepCapped)
	}
	if step > window {
		step = window
	}
	return window, step, warnings, nil
}

// ReadPanel выполняет range-запрос объявленной панели через источник и переводит
// матрицу в нейтральные серии. Недоступность источника — PanelResult с
// Available=false и Reason, а не пустая серия и не ошибка транспорта наружу.
func ReadPanel(ctx context.Context, source QueryRanger, panel Panel, query string, start, end time.Time, window, step time.Duration, limits Limits) PanelResult {
	result := PanelResult{Panel: panel, Range: window, Step: step}
	limits = limits.withDefaults()
	if source == nil {
		result.Reason = ReasonSourceUnavailable
		return result
	}
	matrix, warnings, err := source.QueryRange(ctx, query, start, end, step)
	if err != nil {
		result.Reason = ReasonSourceUnavailable
		return result
	}
	result.Available = true
	result.Series = panelSeries(matrix, limits.MaxPoints)
	result.Warnings = append(result.Warnings, warnings...)
	return result
}

// panelSeries переводит матрицу Prometheus в серии панели, ограничивая число
// точек на серию. Пустая матрица даёт пустой список, а не нулевую серию.
func panelSeries(matrix Matrix, maxPoints int) []PanelSeries {
	if maxPoints <= 0 {
		maxPoints = 300
	}
	out := make([]PanelSeries, 0, len(matrix))
	for _, series := range matrix {
		points := make([]PanelSample, 0, len(series.Samples))
		for index, sample := range series.Samples {
			if index >= maxPoints {
				break
			}
			points = append(points, PanelSample{Time: sample.Time, Value: sample.Value})
		}
		out = append(out, PanelSeries{Labels: series.Metric, Points: points})
	}
	return out
}

// labelValue экранирует значение метки PromQL: обратный слеш и кавычку.
func labelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
