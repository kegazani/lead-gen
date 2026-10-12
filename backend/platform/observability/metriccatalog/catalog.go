// Package metriccatalog содержит неизменяемый технический каталог схем метрик
// процесса и закрытую политику их label values. Приёмники получают один снимок
// каталога и не регистрируют новые имена, значения или tuple во время работы.
package metriccatalog

import (
	"sort"
	"strings"
)

// Kind задаёт тип образца метрики.
type Kind string

const (
	// Counter — монотонный счётчик.
	Counter Kind = "counter"
	// Histogram — распределение наблюдений.
	Histogram Kind = "histogram"
)

// Schema — закрытая схема метрики: имя, тип и точный набор labels.
type Schema struct {
	Name    string
	Kind    Kind
	Labels  []string
	Buckets []float64
	Help    string
}

// Tuple описывает разрешённое сочетание значений связанных меток.
type Tuple struct {
	Labels []string
	Values []string
}

// Catalog — неизменяемый снимок схем, перечней значений и кортежей.
type Catalog struct {
	schemas map[string]Schema
	values  map[string]map[string]struct{}
	tuples  map[string]map[string]struct{}
}

// New копирует все входные данные. Изменения исходных maps/slices после вызова
// не меняют каталог.
func New(schemas []Schema, values map[string][]string, tuples []Tuple) Catalog {
	c := Catalog{
		schemas: make(map[string]Schema, len(schemas)),
		values:  make(map[string]map[string]struct{}, len(values)),
		tuples:  make(map[string]map[string]struct{}),
	}
	for _, schema := range schemas {
		schema.Labels = append([]string(nil), schema.Labels...)
		schema.Buckets = append([]float64(nil), schema.Buckets...)
		c.schemas[schema.Name] = schema
	}
	for label, entries := range values {
		set := make(map[string]struct{}, len(entries))
		for _, value := range entries {
			if value != "" {
				set[value] = struct{}{}
			}
		}
		c.values[label] = set
	}
	for _, tuple := range tuples {
		if len(tuple.Labels) == 0 || len(tuple.Labels) != len(tuple.Values) {
			continue
		}
		names := append([]string(nil), tuple.Labels...)
		sort.Strings(names)
		valuesByName := make(map[string]string, len(tuple.Labels))
		for i, label := range tuple.Labels {
			valuesByName[label] = tuple.Values[i]
		}
		values := make([]string, len(names))
		for i, label := range names {
			values[i] = valuesByName[label]
		}
		key := strings.Join(names, "\x00")
		if c.tuples[key] == nil {
			c.tuples[key] = make(map[string]struct{})
		}
		c.tuples[key][strings.Join(values, "\x00")] = struct{}{}
	}
	return c
}

// Schemas возвращает копию схем, пригодную для регистрации адаптером.
func (c Catalog) Schemas() []Schema {
	out := make([]Schema, 0, len(c.schemas))
	for _, schema := range c.schemas {
		schema.Labels = append([]string(nil), schema.Labels...)
		schema.Buckets = append([]float64(nil), schema.Buckets...)
		out = append(out, schema)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names возвращает имена схем в лексикографическом порядке.
func (c Catalog) Names() []string {
	names := make([]string, 0, len(c.schemas))
	for name := range c.schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Validate проверяет имя и тип метрики, точное множество labels, закрытые
// значения и все tuple-политики, применимые к схеме.
func (c Catalog) Validate(name string, kind Kind, labels map[string]string) bool {
	schema, ok := c.schemas[name]
	if !ok || schema.Kind != kind || len(labels) != len(schema.Labels) {
		return false
	}
	for _, label := range schema.Labels {
		value, present := labels[label]
		if !present || value == "" || len(value) > 128 || strings.Contains(value, "://") {
			return false
		}
		if label == "code" {
			if !validStatusCode(value) {
				return false
			}
			continue
		}
		if _, allowed := c.values[label][value]; !allowed {
			return false
		}
	}
	for tupleKey, allowedTuples := range c.tuples {
		names := strings.Split(tupleKey, "\x00")
		applies := true
		values := make([]string, len(names))
		for i, label := range names {
			value, present := labels[label]
			if !present {
				applies = false
				break
			}
			values[i] = value
		}
		if applies {
			if _, valid := allowedTuples[strings.Join(values, "\x00")]; !valid {
				return false
			}
		}
	}
	return true
}

func validStatusCode(value string) bool {
	if len(value) != 3 || value[0] < '1' || value[0] > '5' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// Default возвращает каталог всех текущих emitters и допустимых label tuples.
// Это общий технический механизм; callers могут передавать его в несколько sinks.
func Default() Catalog {
	buckets := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	schemas := []Schema{
		{Name: "http_requests_total", Kind: Counter, Labels: []string{"service", "code"}, Help: "Число обработанных HTTP-запросов по сервису и коду статуса."},
		{Name: "http_duration_seconds", Kind: Histogram, Labels: []string{"service"}, Buckets: buckets, Help: "Длительность обработки HTTP-запроса, с."},
		{Name: "jobs_total", Kind: Counter, Labels: []string{"service", "outcome"}, Help: "Число фоновых заданий по исходу."},
		{Name: "jobs_duration_seconds", Kind: Histogram, Labels: []string{"service"}, Buckets: buckets, Help: "Длительность обработки фонового задания, с."},
		{Name: "downstream_errors_total", Kind: Counter, Labels: []string{"service", "engine"}, Help: "Число отказов нижестоящего движка по имени движка."},
		{Name: "process_self_terminated_total", Kind: Counter, Help: "Число самозавершений процесса по потере собственной liveness-пробы."},
		{Name: "role_exit_live_total", Kind: Counter, Labels: []string{"service", "role"}, Help: "Число завершений серверной роли при живом процессе."},
		{Name: "port_invocations_total", Kind: Counter, Labels: []string{"port", "adapter", "operation", "outcome"}, Help: "Число вызовов порта по исходу."},
		{Name: "port_invocation_duration_seconds", Kind: Histogram, Labels: []string{"port", "adapter", "operation", "outcome"}, Buckets: buckets, Help: "Длительность вызова порта, с."},
		{Name: "outbox_publish_failures_total", Kind: Counter, Help: "Число отказов публикации записи transactional outbox."},
		{Name: "workflow_started_total", Kind: Counter, Help: "Число запусков исполнения workflow."},
		{Name: "workflow_start_error_total", Kind: Counter, Help: "Число ошибок запуска исполнения workflow."},
		{Name: "workflow_start_already_started_total", Kind: Counter, Help: "Число идемпотентных повторных запусков уже существующего исполнения workflow."},
	}
	values := map[string][]string{
		"service":   {"lead-management", "audience", "outreach-orchestration", "message-delivery", "request-fulfillment", "agent-orchestration", "platform-governance", "data-governance", "integration-platform", "content-production", "analytics", "engagement-observation"},
		"role":      {"api", "worker", "chains", "schedule", "sender", "ingest"},
		"outcome":   {"done", "failed", "cancelled", "success", "failure", "timeout", "canceled"},
		"engine":    {"llm", "search", "crawl", "mail", "analytics"},
		"port":      {"provider-port", "lead-search", "email-generation", "request-intent", "delivery-target", "content-version", "address-verification", "campaign-report", "vector", "mail", "search", "crawl", "llm", "forum"},
		"adapter":   {"http", "inproc", "firecrawl", "openai", "smtp", "temporal"},
		"operation": {"enrich", "search", "generate", "send", "verify", "embed", "query", "publish", "invoke", "crawl", "parse", "resolve"},
	}
	tuples := []Tuple{
		{Labels: []string{"port", "adapter", "operation"}, Values: []string{"lead-search", "http", "search"}},
	}
	return New(schemas, values, tuples)
}
