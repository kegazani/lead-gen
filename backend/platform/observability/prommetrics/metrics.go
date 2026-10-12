// Package prommetrics — адаптер порта observ.Metrics поверх
// prometheus/client_golang. Типы SDK не выходят за пределы пакета: публичная
// поверхность — это реализация порта (Incr/Observe) и HTTP-хендлер экспозиции
// /metrics, а registry и векторы спрятаны за неэкспортированными полями.
//
// Имена метрик и границы гистограмм заданы в одном месте (specs): смена движка
// метрик не трогает вызовы через порт observ.Metrics (design.md, Решение 1).
package prommetrics

import (
	"math"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"lead-gen/backend/platform/observability/metriccatalog"
)

// Adapter — реализация observ.Metrics поверх prometheus/client_golang. Все
// метрики регистрируются в одном Registry процесса, поэтому один хендлер
// /metrics агрегирует метрики всех сервисов группы.
type Adapter struct {
	registry   *prometheus.Registry
	catalog    metriccatalog.Catalog
	counters   map[string]*prometheus.CounterVec
	histograms map[string]*prometheus.HistogramVec
	labels     map[string][]string
}

// New собирает адаптер с реальными счётчиками и гистограммами (включая
// runtime-метрики Go и процесса). Метрики из specs регистрируются сразу, чтобы
// /metrics отдавал их даже до первого наблюдения.
func New() *Adapter {
	return NewWithCatalog(metriccatalog.Default())
}

// NewWithCatalog собирает адаптер по общему инъецированному каталогу схем,
// значений и кортежей.
func NewWithCatalog(catalog metriccatalog.Catalog) *Adapter {
	a := &Adapter{
		registry:   prometheus.NewRegistry(),
		catalog:    catalog,
		counters:   make(map[string]*prometheus.CounterVec),
		histograms: make(map[string]*prometheus.HistogramVec),
		labels:     make(map[string][]string),
	}
	a.registry.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	for _, s := range catalog.Schemas() {
		a.labels[s.Name] = s.Labels
		switch s.Kind {
		case metriccatalog.Counter:
			vec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: s.Name, Help: s.Help}, s.Labels)
			a.registry.MustRegister(vec)
			a.counters[s.Name] = vec
		case metriccatalog.Histogram:
			vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: s.Name, Help: s.Help, Buckets: s.Buckets}, s.Labels)
			a.registry.MustRegister(vec)
			a.histograms[s.Name] = vec
		}
	}
	return a
}

// Handler возвращает HTTP-хендлер экспозиции /metrics (Prometheus exposition
// format, text/plain; version=0.0.4) поверх общего Registry.
func (a *Adapter) Handler() http.Handler {
	return promhttp.HandlerFor(a.registry, promhttp.HandlerOpts{})
}

// Incr реализует observ.Metrics. Недопустимые метки или неизвестное имя
// отбрасываются без создания серии (design.md, Решение 6).
func (a *Adapter) Incr(name string, labels map[string]string) {
	if !a.catalog.Validate(name, metriccatalog.Counter, labels) {
		return
	}
	vec, ok := a.counters[name]
	if !ok {
		return
	}
	vals, ok := labelValues(a.labels[name], labels)
	if !ok {
		return
	}
	vec.WithLabelValues(vals...).Inc()
}

// Observe реализует observ.Metrics. Недопустимые метки или неизвестное имя
// отбрасываются без создания серии.
func (a *Adapter) Observe(name string, v float64, labels map[string]string) {
	if math.IsNaN(v) || math.IsInf(v, 0) || !a.catalog.Validate(name, metriccatalog.Histogram, labels) {
		return
	}
	vec, ok := a.histograms[name]
	if !ok {
		return
	}
	vals, ok := labelValues(a.labels[name], labels)
	if !ok {
		return
	}
	vec.WithLabelValues(vals...).Observe(v)
}

// MetricNames возвращает имена всех метрик, объявленных в каталоге specs. Нужен
// тестам-инвариантам «всё, что эмитит порт Metrics, объявлено в каталоге»: имя
// вне specs молча отбрасывается Incr/Observe, поэтому рассинхронизация имён
// означала бы тихую потерю метрик.
func MetricNames() []string {
	return metriccatalog.Default().Names()
}

// labelValues раскладывает переданные метки в порядке, объявленном у метрики.
// Набор меток обязан совпадать с объявленным (по числу и именам), иначе Prometheus
// упал бы на WithLabelValues; несовпадение отбрасывается честно.
func labelValues(order []string, labels map[string]string) ([]string, bool) {
	if len(order) != len(labels) {
		return nil, false
	}
	vals := make([]string, len(order))
	for i, name := range order {
		v, present := labels[name]
		if !present {
			return nil, false
		}
		vals[i] = v
	}
	return vals, true
}
