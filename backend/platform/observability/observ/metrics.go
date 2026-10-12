package observ

import (
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"

	"lead-gen/backend/platform/observability/metriccatalog"
)

// Metrics — приёмник технических метрик сервиса. Метрики RED (rate, errors,
// duration) и производные (глубина очереди, доля отказов downstream) кодируются
// парой Incr/Observe, поэтому примитив не зависит от конкретного бэкенда.
type Metrics interface {
	// Incr увеличивает счётчик name с набором меток на единицу.
	Incr(name string, labels map[string]string)
	// Observe добавляет наблюдение v к гистограмме name с набором меток.
	Observe(name string, v float64, labels map[string]string)
}

// sampleKey — канонический ключ образца метрики: имя и отсортированные метки.
type sampleKey struct {
	name   string
	labels string
}

// observation — накопленные сумма и число наблюдений одного образца.
type observation struct {
	sum   float64
	count int64
}

// MemoryMetrics — потокобезопасная in-memory реализация Metrics для тестов и
// локального запуска без внешнего бэкенда. Форма совпадает с реальным
// приёмником, поэтому подмена реализации не меняет вызовы.
type MemoryMetrics struct {
	mu           sync.Mutex
	catalog      metriccatalog.Catalog
	counters     map[sampleKey]int64
	observations map[sampleKey]observation
}

// NewMetrics возвращает пустой потокобезопасный приёмник метрик.
func NewMetrics() Metrics {
	return NewMetricsWithCatalog(metriccatalog.Default())
}

// NewMetricsWithCatalog создаёт приёмник, применяющий переданный закрытый каталог
// схем, label values и tuple policy.
func NewMetricsWithCatalog(catalog metriccatalog.Catalog) *MemoryMetrics {
	return &MemoryMetrics{
		catalog:      catalog,
		counters:     make(map[sampleKey]int64),
		observations: make(map[sampleKey]observation),
	}
}

// Incr реализует Metrics. Образец с недопустимой меткой не учитывается: он не
// создаёт временную серию (design.md, Решение 6).
func (m *MemoryMetrics) Incr(name string, labels map[string]string) {
	if !m.catalog.Validate(name, metriccatalog.Counter, labels) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[newSampleKey(name, labels)]++
}

// Observe реализует Metrics. Образец с недопустимой меткой не учитывается.
func (m *MemoryMetrics) Observe(name string, v float64, labels map[string]string) {
	if math.IsNaN(v) || math.IsInf(v, 0) || !m.catalog.Validate(name, metriccatalog.Histogram, labels) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := newSampleKey(name, labels)
	o := m.observations[k]
	o.sum += v
	o.count++
	m.observations[k] = o
}

// Counter возвращает накопленное значение счётчика. Метод нужен тестам и
// локальной диагностике: интерфейс Metrics остаётся минимальным.
func (m *MemoryMetrics) Counter(name string, labels map[string]string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[newSampleKey(name, labels)]
}

// Observation возвращает сумму и число наблюдений гистограммы.
func (m *MemoryMetrics) Observation(name string, labels map[string]string) (sum float64, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.observations[newSampleKey(name, labels)]
	return o.sum, o.count
}

// newSampleKey канонизирует метки: порядок ключей в map не определён, поэтому
// метки сортируются и кодируются, иначе одинаковые наборы дали бы разные ключи.
func newSampleKey(name string, labels map[string]string) sampleKey {
	if len(labels) == 0 {
		return sampleKey{name: name}
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(url.QueryEscape(k))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(labels[k]))
	}
	return sampleKey{name: name, labels: b.String()}
}
