package domain

import (
	"errors"
	"testing"
)

func availableMetric(t *testing.T, kind MetricKind, value float64) Metric {
	t.Helper()
	m, err := NewAvailableMetric(kind, value, ConfidenceMeasured, "unit", "2026-01", "source-a", baseTime)
	if err != nil {
		t.Fatalf("NewAvailableMetric: %v", err)
	}
	return m
}

func TestNewUnavailableMetric_HasNoValue(t *testing.T) {
	m, err := NewUnavailableMetric(MetricTraffic, "provider-x", baseTime)
	if err != nil {
		t.Fatalf("NewUnavailableMetric: %v", err)
	}
	if m.Confidence != ConfidenceUnavailable {
		t.Fatalf("признак = %q", m.Confidence)
	}
	if m.Value != nil || m.Range != nil {
		t.Fatalf("недоступная метрика не должна нести значение: %+v", m)
	}
	if _, ok := m.NumericValue(); ok {
		t.Fatal("недоступная метрика не должна давать числовое значение (ноль не подставляется)")
	}
}

func TestNewAvailableMetric_RequiresSourceAndUnit(t *testing.T) {
	if _, err := NewAvailableMetric(MetricTraffic, 100, ConfidenceMeasured, "", "p", "s", baseTime); !errors.Is(err, ErrInvalidMetric) {
		t.Fatalf("без единицы: ошибка = %v, ожидалась ErrInvalidMetric", err)
	}
	if _, err := NewAvailableMetric(MetricTraffic, 100, ConfidenceUnavailable, "u", "p", "s", baseTime); !errors.Is(err, ErrInvalidMetric) {
		t.Fatalf("unavailable со значением: ошибка = %v, ожидалась ErrInvalidMetric", err)
	}
	if _, err := NewRangeMetric(MetricInstalls, MetricRange{Low: 10, High: 5}, ConfidenceEstimated, "u", "p", "s", baseTime); !errors.Is(err, ErrInvalidMetric) {
		t.Fatalf("переставленный диапазон: ошибка = %v, ожидалась ErrInvalidMetric", err)
	}
}

func TestRangeMetric_UsesMidpoint(t *testing.T) {
	m, err := NewRangeMetric(MetricInstalls, MetricRange{Low: 100, High: 500}, ConfidenceEstimated, "installs", "2026", "play", baseTime)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := m.NumericValue()
	if !ok || value != 300 {
		t.Fatalf("значение = %v/%v, ожидалось 300", value, ok)
	}
}

func TestReconcileMetricSources_DivergenceShowsRange(t *testing.T) {
	low, err := NewAvailableMetric(MetricTraffic, 1000, ConfidenceEstimated, "visits", "2026-01", "source-a", baseTime)
	if err != nil {
		t.Fatal(err)
	}
	high, err := NewAvailableMetric(MetricTraffic, 2000, ConfidenceEstimated, "visits", "2026-01", "source-b", baseTime)
	if err != nil {
		t.Fatal(err)
	}
	divergence, err := ReconcileMetricSources([]Metric{low, high}, 0.2)
	if err != nil {
		t.Fatalf("ReconcileMetricSources: %v", err)
	}
	if !divergence.Diverged {
		t.Fatal("ожидалось расхождение выше порога")
	}
	if divergence.Low != 1000 || divergence.High != 2000 {
		t.Fatalf("диапазон = %v..%v, ожидался 1000..2000", divergence.Low, divergence.High)
	}
	if divergence.Low == 1500 || divergence.High == 1500 {
		t.Fatal("усреднение запрещено: 1500 не должно встречаться")
	}
	if len(divergence.Sources) != 2 {
		t.Fatalf("источники = %v", divergence.Sources)
	}
}

func TestReconcileMetricSources_BelowThresholdSingleValue(t *testing.T) {
	a, err := NewAvailableMetric(MetricTraffic, 1000, ConfidenceMeasured, "visits", "2026-01", "source-a", baseTime)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewAvailableMetric(MetricTraffic, 1005, ConfidenceEstimated, "visits", "2026-01", "source-b", baseTime)
	if err != nil {
		t.Fatal(err)
	}
	divergence, err := ReconcileMetricSources([]Metric{a, b}, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if divergence.Diverged {
		t.Fatal("расхождение ниже порога не должно считаться расхождением")
	}
	if divergence.Low != divergence.High {
		t.Fatalf("ниже порога ожидалось одно значение, got %v..%v", divergence.Low, divergence.High)
	}
}

func TestDefaultSizeWeights_MatchDesign(t *testing.T) {
	w := DefaultSizeWeights()
	if w.Traffic != 0.35 || w.Installs != 0.25 || w.Employees != 0.15 || w.Products != 0.15 || w.DomainAge != 0.10 {
		t.Fatalf("веса не совпадают с design.md: %+v", w)
	}
}

func TestAssessSize_Complete(t *testing.T) {
	metrics := []Metric{
		availableMetric(t, MetricTraffic, 2_500_000),
		availableMetric(t, MetricInstalls, 5_000_000),
		availableMetric(t, MetricEmployees, 2_500),
		availableMetric(t, MetricProducts, 25),
		availableMetric(t, MetricDomainAge, 10),
	}
	assessment, err := AssessSize(metrics, DefaultSizeConfig())
	if err != nil {
		t.Fatalf("AssessSize: %v", err)
	}
	if !assessment.Complete || len(assessment.MissingKinds) != 0 {
		t.Fatalf("оценка должна быть полной: %+v", assessment)
	}
	if assessment.Score < 0.49 || assessment.Score > 0.51 {
		t.Fatalf("оценка = %v, ожидалась ~0.5", assessment.Score)
	}
	if assessment.Class != SizeSmall {
		t.Fatalf("класс = %q, ожидался small", assessment.Class)
	}
	if len(assessment.Contributions) != 5 {
		t.Fatalf("вкладов = %d, ожидалось 5", len(assessment.Contributions))
	}
}

func TestAssessSize_UnavailableNotSubstitutedByZero(t *testing.T) {
	unavailable, err := NewUnavailableMetric(MetricTraffic, "provider-x", baseTime)
	if err != nil {
		t.Fatal(err)
	}
	metrics := []Metric{
		unavailable,
		availableMetric(t, MetricInstalls, 5_000_000),
		availableMetric(t, MetricEmployees, 2_500),
		availableMetric(t, MetricProducts, 25),
		availableMetric(t, MetricDomainAge, 10),
	}
	assessment, err := AssessSize(metrics, DefaultSizeConfig())
	if err != nil {
		t.Fatalf("AssessSize: %v", err)
	}
	if assessment.Complete {
		t.Fatal("оценка с недоступной метрикой должна быть неполной")
	}
	if len(assessment.MissingKinds) != 1 || assessment.MissingKinds[0] != MetricTraffic {
		t.Fatalf("недоступные признаки = %v, ожидался traffic", assessment.MissingKinds)
	}
	// Оценка перенормирована на доступные признаки; недоступный трафик не обнулён.
	if assessment.Score < 0.49 || assessment.Score > 0.51 {
		t.Fatalf("оценка = %v, ожидалась ~0.5 (без подстановки нуля)", assessment.Score)
	}
}

func TestAssessSize_NoData(t *testing.T) {
	if _, err := AssessSize(nil, DefaultSizeConfig()); !errors.Is(err, ErrNoSizeData) {
		t.Fatalf("ошибка = %v, ожидалась ErrNoSizeData", err)
	}
}

func TestAssessSize_DuplicateMetric(t *testing.T) {
	metrics := []Metric{availableMetric(t, MetricTraffic, 100), availableMetric(t, MetricTraffic, 200)}
	if _, err := AssessSize(metrics, DefaultSizeConfig()); !errors.Is(err, ErrDuplicateMetric) {
		t.Fatalf("ошибка = %v, ожидалась ErrDuplicateMetric", err)
	}
}

func TestClassifySize_Boundaries(t *testing.T) {
	thresholds := DefaultSizeConfig().Thresholds
	cases := []struct {
		score float64
		want  SizeClass
	}{
		{0.10, SizeMicro}, {0.25, SizeMicro}, {0.30, SizeSmall},
		{0.50, SizeSmall}, {0.60, SizeMedium}, {0.75, SizeMedium}, {0.90, SizeLarge},
	}
	for _, tc := range cases {
		if got := classifySize(tc.score, thresholds); got != tc.want {
			t.Fatalf("score %v: got %q, ожидался %q", tc.score, got, tc.want)
		}
	}
}

func TestSizeConfig_WeightsChangeClass(t *testing.T) {
	metrics := []Metric{availableMetric(t, MetricTraffic, 5_000_000)}
	cfg := DefaultSizeConfig()
	assessment, err := AssessSize(metrics, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Class != SizeLarge {
		t.Fatalf("полный трафик: класс = %q, ожидался large", assessment.Class)
	}
	// Изменение порога пересчитывает класс без правки кода.
	cfg.Thresholds = SizeThresholds{Micro: 1.0, Small: 1.0, Medium: 1.0}
	assessment, err = AssessSize(metrics, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Class != SizeMicro {
		t.Fatalf("после сдвига порогов: класс = %q, ожидался micro", assessment.Class)
	}
}
