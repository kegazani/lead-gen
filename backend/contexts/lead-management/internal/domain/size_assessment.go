package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Confidence — признак достоверности метрики (value object confidence-score
// capability company-size-assessment): измерено, оценка или недоступно.
type Confidence string

// Согласованные признаки достоверности.
const (
	// ConfidenceMeasured — значение получено из первоисточника.
	ConfidenceMeasured Confidence = "measured"
	// ConfidenceEstimated — значение является оценкой сервиса.
	ConfidenceEstimated Confidence = "estimated"
	// ConfidenceUnavailable — значение получить не удалось: ноль не подставляется.
	ConfidenceUnavailable Confidence = "unavailable"
)

// Known сообщает, входит ли признак в согласованный набор.
func (c Confidence) Known() bool {
	switch c {
	case ConfidenceMeasured, ConfidenceEstimated, ConfidenceUnavailable:
		return true
	default:
		return false
	}
}

// HasValue сообщает, несёт ли признак значение. У `unavailable` значения нет:
// недоступность — не измерение.
func (c Confidence) HasValue() bool {
	return c == ConfidenceMeasured || c == ConfidenceEstimated
}

// MetricKind — признак размера компании. Набор закрыт.
type MetricKind string

// Признаки, входящие в интегральную оценку (design.md, Решение 5).
const (
	MetricTraffic   MetricKind = "traffic"
	MetricInstalls  MetricKind = "installs"
	MetricEmployees MetricKind = "employees"
	MetricProducts  MetricKind = "products"
	MetricDomainAge MetricKind = "domain-age"
)

// Known сообщает, входит ли признак в согласованный набор.
func (k MetricKind) Known() bool {
	switch k {
	case MetricTraffic, MetricInstalls, MetricEmployees, MetricProducts, MetricDomainAge:
		return true
	default:
		return false
	}
}

// MetricRange — диапазон значения (например, установки Google Play публикуются
// диапазоном, а не числом).
type MetricRange struct {
	Low  float64
	High float64
}

// Metric — маркированная метрика: значение (или диапазон), единица, период,
// источник, дата получения и признак достоверности. Значение и признак
// достоверности согласованы: у `unavailable` значения нет.
type Metric struct {
	Kind        MetricKind
	Value       *float64
	Range       *MetricRange
	Unit        string
	Period      string
	Source      string
	SourceURL   string
	RetrievedAt time.Time
	Confidence  Confidence
}

// NewAvailableMetric создаёт метрику с числовым значением. Признак обязан нести
// значение (measured/estimated); единица и источник обязательны.
func NewAvailableMetric(kind MetricKind, value float64, confidence Confidence, unit, period, source string, retrievedAt time.Time) (Metric, error) {
	return newMetric(kind, &value, nil, confidence, unit, period, source, retrievedAt)
}

// NewRangeMetric создаёт метрику с диапазоном значения.
func NewRangeMetric(kind MetricKind, r MetricRange, confidence Confidence, unit, period, source string, retrievedAt time.Time) (Metric, error) {
	if r.High < r.Low {
		return Metric{}, fmt.Errorf("%w: границы диапазона переставлены", ErrInvalidMetric)
	}
	m, err := newMetric(kind, nil, &r, confidence, unit, period, source, retrievedAt)
	if err != nil {
		return Metric{}, err
	}
	return m, nil
}

// NewUnavailableMetric создаёт метрику без значения: ноль запрещён, признак
// `unavailable`.
func NewUnavailableMetric(kind MetricKind, source string, retrievedAt time.Time) (Metric, error) {
	if !kind.Known() {
		return Metric{}, fmt.Errorf("%w: %q", ErrUnknownMetricKind, kind)
	}
	if strings.TrimSpace(source) == "" {
		return Metric{}, fmt.Errorf("%w: источник обязателен", ErrInvalidMetric)
	}
	if retrievedAt.IsZero() {
		return Metric{}, fmt.Errorf("%w: дата получения обязательна", ErrInvalidMetric)
	}
	return Metric{
		Kind:        kind,
		Source:      strings.TrimSpace(source),
		RetrievedAt: retrievedAt.UTC(),
		Confidence:  ConfidenceUnavailable,
	}, nil
}

// newMetric собирает метрику со значением или диапазоном и проверяет согласованность.
func newMetric(kind MetricKind, value *float64, r *MetricRange, confidence Confidence, unit, period, source string, retrievedAt time.Time) (Metric, error) {
	if !kind.Known() {
		return Metric{}, fmt.Errorf("%w: %q", ErrUnknownMetricKind, kind)
	}
	if !confidence.Known() || !confidence.HasValue() {
		return Metric{}, fmt.Errorf("%w: признак %q не несёт значения", ErrInvalidMetric, confidence)
	}
	if value == nil && r == nil {
		return Metric{}, fmt.Errorf("%w: значение не задано", ErrInvalidMetric)
	}
	if value != nil && r != nil {
		return Metric{}, fmt.Errorf("%w: заданы и значение, и диапазон", ErrInvalidMetric)
	}
	if value != nil && *value < 0 {
		return Metric{}, fmt.Errorf("%w: отрицательное значение", ErrInvalidMetric)
	}
	if strings.TrimSpace(unit) == "" {
		return Metric{}, fmt.Errorf("%w: единица измерения обязательна", ErrInvalidMetric)
	}
	if strings.TrimSpace(source) == "" {
		return Metric{}, fmt.Errorf("%w: источник обязателен", ErrInvalidMetric)
	}
	if retrievedAt.IsZero() {
		return Metric{}, fmt.Errorf("%w: дата получения обязательна", ErrInvalidMetric)
	}
	return Metric{
		Kind:        kind,
		Value:       value,
		Range:       r,
		Unit:        strings.TrimSpace(unit),
		Period:      strings.TrimSpace(period),
		Source:      strings.TrimSpace(source),
		RetrievedAt: retrievedAt.UTC(),
		Confidence:  confidence,
	}, nil
}

// NumericValue возвращает представительное значение метрики: число, либо середину
// диапазона. Второй результат — false для недоступной метрики (значения нет).
func (m Metric) NumericValue() (float64, bool) {
	if !m.Confidence.HasValue() {
		return 0, false
	}
	if m.Value != nil {
		return *m.Value, true
	}
	if m.Range != nil {
		return (m.Range.Low + m.Range.High) / 2, true
	}
	return 0, false
}

// SourceDivergence — расхождение значений из разных источников. Усреднение
// запрещено: при расхождении выше порога показывается диапазон с обоими
// источниками.
type SourceDivergence struct {
	Low      float64
	High     float64
	Sources  []string
	Diverged bool
}

// ReconcileMetricSources сравнивает значения из разных источников. Расхождение
// выше порога (относительное, от максимума) даёт диапазон; ниже — диапазон из
// одного значения. Среднее не вычисляется ни в одном случае.
func ReconcileMetricSources(metrics []Metric, threshold float64) (SourceDivergence, error) {
	type sample struct {
		value  float64
		source string
	}
	var samples []sample
	seen := map[string]bool{}
	for _, m := range metrics {
		value, ok := m.NumericValue()
		if !ok {
			continue
		}
		if seen[m.Source] {
			continue
		}
		seen[m.Source] = true
		samples = append(samples, sample{value: value, source: m.Source})
	}
	if len(samples) == 0 {
		return SourceDivergence{}, fmt.Errorf("%w: нет значений для сравнения", ErrInvalidMetric)
	}
	low := samples[0].value
	high := samples[0].value
	for _, s := range samples[1:] {
		low = math.Min(low, s.value)
		high = math.Max(high, s.value)
	}
	if len(samples) == 1 {
		return SourceDivergence{Low: low, High: high, Sources: []string{samples[0].source}}, nil
	}
	diverged := false
	if high > 0 {
		diverged = (high-low)/high > threshold
	} else {
		diverged = low != high
	}
	sources := make([]string, 0, len(samples))
	for _, s := range samples {
		sources = append(sources, s.source)
	}
	if !diverged {
		// Расхождение ниже порога: одно значение. Среднее не формируется даже здесь:
		// берётся значение с наибольшей достоверностью (измеренное предпочтительнее
		// оценки), при равенстве — первое.
		low, high = representative(metrics, samples[0].value)
	}
	return SourceDivergence{Low: low, High: high, Sources: sources, Diverged: diverged}, nil
}

// representative выбирает одно значение при расхождении ниже порога: измеренное
// предпочтительнее оценки, иначе — значение по умолчанию.
func representative(metrics []Metric, fallback float64) (float64, float64) {
	best := fallback
	found := false
	for _, m := range metrics {
		value, ok := m.NumericValue()
		if !ok || m.Confidence != ConfidenceMeasured {
			continue
		}
		best = value
		found = true
		break
	}
	if !found {
		return fallback, fallback
	}
	return best, best
}

// SizeClass — размерный класс компании.
type SizeClass string

// Согласованный набор размерных классов.
const (
	SizeMicro  SizeClass = "micro"
	SizeSmall  SizeClass = "small"
	SizeMedium SizeClass = "medium"
	SizeLarge  SizeClass = "large"
)

// SizeWeights — веса признаков интегральной оценки. Сумма не нормируется: оценка
// делится на сумму весов доступных признаков.
type SizeWeights struct {
	Traffic   float64
	Installs  float64
	Employees float64
	Products  float64
	DomainAge float64
}

// DefaultSizeWeights возвращает веса по умолчанию (design.md, Решение 5).
func DefaultSizeWeights() SizeWeights {
	return SizeWeights{Traffic: 0.35, Installs: 0.25, Employees: 0.15, Products: 0.15, DomainAge: 0.10}
}

// weightOf возвращает вес признака.
func (w SizeWeights) weightOf(kind MetricKind) float64 {
	switch kind {
	case MetricTraffic:
		return w.Traffic
	case MetricInstalls:
		return w.Installs
	case MetricEmployees:
		return w.Employees
	case MetricProducts:
		return w.Products
	case MetricDomainAge:
		return w.DomainAge
	default:
		return 0
	}
}

// Normalization — границы нормирования признака в диапазон 0..1. Границы —
// предмет калибровки, а не измеренный факт.
type Normalization struct {
	Min float64
	Max float64
}

// SizeThresholds — верхние границы интегральной оценки для классов micro, small и
// medium; выше medium — large. Пороги — предмет калибровки (design.md, Решение 5):
// до калибровки помечаются непроверенными.
type SizeThresholds struct {
	Micro  float64
	Small  float64
	Medium float64
}

// SizeConfig — настраиваемая модель оценки: веса, границы нормирования и пороги
// классов. Изменение весов и порогов пересчитывает оценку и класс без правки кода.
type SizeConfig struct {
	Weights    SizeWeights
	Bounds     map[MetricKind]Normalization
	Thresholds SizeThresholds
}

// DefaultSizeConfig возвращает документированные значения по умолчанию. Пороги и
// границы непроверены до калибровки на реальных данных (BRD §8, вопрос 16).
func DefaultSizeConfig() SizeConfig {
	return SizeConfig{
		Weights: DefaultSizeWeights(),
		Bounds: map[MetricKind]Normalization{
			MetricTraffic:   {Min: 0, Max: 5_000_000},
			MetricInstalls:  {Min: 0, Max: 10_000_000},
			MetricEmployees: {Min: 0, Max: 5_000},
			MetricProducts:  {Min: 0, Max: 50},
			MetricDomainAge: {Min: 0, Max: 20},
		},
		Thresholds: SizeThresholds{Micro: 0.25, Small: 0.50, Medium: 0.75},
	}
}

// MetricContribution — вклад признака в интегральную оценку: нормированное
// значение, вес и произведение. Обоснование класса для оператора.
type MetricContribution struct {
	Kind         MetricKind
	Normalized   float64
	Weight       float64
	Contribution float64
}

// SizeAssessment — интегральная оценка и размерный класс с обоснованием. Complete
// — все признаки с ненулевым весом доступны; при неполноте класс рассчитан по
// доступным признакам и помечен.
type SizeAssessment struct {
	Score         float64
	Class         SizeClass
	Complete      bool
	Contributions []MetricContribution
	MissingKinds  []MetricKind
}

// AssessSize сводит метрики в интегральную оценку и размерный класс. Недоступные
// признаки не подменяются нулём: они пропускаются, веса перенормируются на
// доступные, а оценка помечается неполной.
func AssessSize(metrics []Metric, cfg SizeConfig) (SizeAssessment, error) {
	byKind := make(map[MetricKind]Metric, len(metrics))
	for _, m := range metrics {
		if _, ok := byKind[m.Kind]; ok {
			return SizeAssessment{}, fmt.Errorf("%w: %q", ErrDuplicateMetric, m.Kind)
		}
		byKind[m.Kind] = m
	}

	var contributions []MetricContribution
	var totalWeight float64
	var missing []MetricKind
	for _, kind := range []MetricKind{MetricTraffic, MetricInstalls, MetricEmployees, MetricProducts, MetricDomainAge} {
		weight := cfg.Weights.weightOf(kind)
		if weight <= 0 {
			continue
		}
		metric, ok := byKind[kind]
		if !ok {
			missing = append(missing, kind)
			continue
		}
		value, hasValue := metric.NumericValue()
		if !hasValue {
			// Недоступная метрика не подставляется нулём.
			missing = append(missing, kind)
			continue
		}
		bound, ok := cfg.Bounds[kind]
		if !ok || bound.Max <= bound.Min {
			return SizeAssessment{}, fmt.Errorf("%w: границы нормирования для %q", ErrInvalidSizeConfig, kind)
		}
		normalized := clamp01((value - bound.Min) / (bound.Max - bound.Min))
		contribution := weight * normalized
		contributions = append(contributions, MetricContribution{
			Kind:         kind,
			Normalized:   normalized,
			Weight:       weight,
			Contribution: contribution,
		})
		totalWeight += weight
	}

	if totalWeight <= 0 {
		return SizeAssessment{}, fmt.Errorf("%w: нет доступных метрик", ErrNoSizeData)
	}

	score := 0.0
	for _, c := range contributions {
		score += c.Contribution
	}
	score = clamp01(score / totalWeight)

	return SizeAssessment{
		Score:         score,
		Class:         classifySize(score, cfg.Thresholds),
		Complete:      len(missing) == 0,
		Contributions: contributions,
		MissingKinds:  missing,
	}, nil
}

// classifySize относит оценку к размерному классу по верхним границам.
func classifySize(score float64, thresholds SizeThresholds) SizeClass {
	switch {
	case score <= thresholds.Micro:
		return SizeMicro
	case score <= thresholds.Small:
		return SizeSmall
	case score <= thresholds.Medium:
		return SizeMedium
	default:
		return SizeLarge
	}
}

// clamp01 ограничивает значение диапазоном 0..1.
func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
