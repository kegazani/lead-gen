package httpapi

import (
	"context"
	"time"

	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/workflow"
)

// Категории процесса — машинные коды среза, к которому ведёт цифра. Значения
// используются и в разметке ссылок, и в фильтре ленты, поэтому заданы константами.
const (
	// CategoryFound — найдено компаний.
	CategoryFound = "found"
	// CategoryProcessed — обработано компаний.
	CategoryProcessed = "processed"
	// CategoryRejected — отклонено компаний.
	CategoryRejected = "rejected"
	// CategoryInProgress — в работе сейчас.
	CategoryInProgress = "in_progress"
	// CategoryResult — результат процесса (отправлено, ответили).
	CategoryResult = "result"
)

// processCategories — порядок вывода цифр процесса.
var processCategories = []string{CategoryFound, CategoryProcessed, CategoryRejected, CategoryInProgress, CategoryResult}

// ClientNumbers — цифры процесса по одному клиенту. Только обезличенные агрегаты.
type ClientNumbers struct {
	ClientID   string
	Found      int
	Processed  int
	Rejected   int
	InProgress int
	Result     int
}

// NumberPeriod — цифры процесса за один день (разбивка по времени). Представлены
// только категории, у которых есть временное измерение в источнике (владелец
// analytics): найдено, обработано, отклонено.
type NumberPeriod struct {
	Date      string
	Found     int
	Processed int
	Rejected  int
}

// ClientNumbersResult — цифры процесса: срез по клиентам и разбивка по времени.
// Пустые срезы — корректный результат, отличимый от недоступности владельца.
type ClientNumbersResult struct {
	Clients []ClientNumbers
	Periods []NumberPeriod
}

// Total складывает цифры клиентов в сводные.
func totalNumbers(clients []ClientNumbers) ClientNumbers {
	var total ClientNumbers
	for _, client := range clients {
		total.Found += client.Found
		total.Processed += client.Processed
		total.Rejected += client.Rejected
		total.InProgress += client.InProgress
		total.Result += client.Result
	}
	return total
}

// value возвращает цифру категории.
func (n ClientNumbers) value(category string) int {
	switch category {
	case CategoryFound:
		return n.Found
	case CategoryProcessed:
		return n.Processed
	case CategoryRejected:
		return n.Rejected
	case CategoryInProgress:
		return n.InProgress
	case CategoryResult:
		return n.Result
	default:
		return 0
	}
}

// AgentFilter — фильтр ленты агентов (тенант, срез по клиенту и категории).
// TenantID обязателен: запуск одного тенанта не виден другому, и выборка
// скоупится тенантом запроса.
type AgentFilter struct {
	TenantID string
	ClientID string
	Category string
	Limit    int
}

// AgentRow — строка живой ленты агентов. Компания — название из зелёной зоны;
// персональных данных и содержимого красной зоны здесь нет.
type AgentRow struct {
	RunID     string
	ClientID  string
	Company   string
	State     string
	Step      string
	UpdatedAt time.Time
}

// AgentEvent — событие жизненного цикла шага агента. Reason — нормализованный код
// причины, а не свободный текст владельца: секреты и ПД сюда не попадают.
type AgentEvent struct {
	At      time.Time
	Step    string
	Kind    string
	Outcome string
	Reason  string
}

// AgentReason — нормализованная причина отказа с числом.
type AgentReason struct {
	Code  string
	Count int
}

// AgentJob — связанное задание (идентификатор, вид, состояние).
type AgentJob struct {
	ID    string
	Kind  string
	State string
}

// AgentDetail — подробности агента/шага: события, причины отказов, связанные
// задания. Отказ шага приходит исходом failed, а не маскируется под успех.
type AgentDetail struct {
	RunID    string
	ClientID string
	Company  string
	State    string
	Events   []AgentEvent
	Reasons  []AgentReason
	Jobs     []AgentJob
}

// ClientAggregate — агрегат по клиенту для полноэкранного дашборда. Anomalies —
// машинные коды выявленных аномалий.
type ClientAggregate struct {
	ClientID   string
	Found      int
	Processed  int
	Rejected   int
	InProgress int
	Result     int
	Anomalies  []string
}

// numbers возвращает цифры агрегата в обезличенном виде.
func (a ClientAggregate) numbers() ClientNumbers {
	return ClientNumbers{Found: a.Found, Processed: a.Processed, Rejected: a.Rejected, InProgress: a.InProgress, Result: a.Result}
}

// value возвращает цифру категории агрегата.
func (a ClientAggregate) value(category string) int {
	return a.numbers().value(category)
}

// OperationsData — источник цифр, ленты и агрегатов операционного экрана. nil
// означает, что владелец данных (контекст agent-orchestration и аналитика) не
// подключён: экран честно показывает «недоступно», а не нули и не пустой успех.
type OperationsData interface {
	Numbers(ctx context.Context, window time.Duration) (ClientNumbersResult, error)
	Agents(ctx context.Context, filter AgentFilter) ([]AgentRow, error)
	// AgentDetail читает подробности запуска в контексте тенанта: контракт
	// agent-run-state требует tenant_id, и изоляция тенантов сохраняется.
	AgentDetail(ctx context.Context, tenantID, runID string) (AgentDetail, error)
	Clients(ctx context.Context, window time.Duration) ([]ClientAggregate, error)
}

// WorkflowReader — серверное чтение процессов durable-исполнения (Temporal).
// *workflow.Client удовлетворяет интерфейсу. nil — рантайм не подключён: граф
// честно показывает «недоступно».
type WorkflowReader interface {
	List(ctx context.Context, query string, limit int) ([]workflow.ProcessExecution, error)
	Timeline(ctx context.Context, workflowID, runID string) (workflow.Process, error)
}

// RequestSummary — минимальная строка из списка заявок владельца.
type RequestSummary struct {
	RequestID    string
	DomainStatus string
}

// RequestExecution — данные исполнения конкретной заявки. ExecutionFound
// отличает отсутствие записи исполнения от исполнения с фактическими нулевыми
// счётчиками.
type RequestExecution struct {
	RequestID      string
	DomainStatus   string
	WorkflowID     string
	RunID          string
	Phase          string
	ExecutionFound bool
	Found          int
	Target         int
}

// RequestExecutionReader — read-only источник заявок и их исполнения. Реализация
// запрашивает список у владельца заявок, затем читает workflow по точному
// requestID; интерфейс не требует разбирать WorkflowID для определения заявки.
type RequestExecutionReader interface {
	ListRequests(ctx context.Context, tenantID string) ([]RequestSummary, error)
	Workflow(ctx context.Context, tenantID, requestID string) (RequestExecution, error)
}

// OperationsMetricPanels — объявленный набор метрик-панелей экрана операций.
// Запросы опираются на реально публикуемые метрики (middleware наблюдаемости и
// счётчики клиента Temporal); произвольный PromQL от клиента не принимается.
func OperationsMetricPanels() []prometheus.Panel {
	return []prometheus.Panel{
		{
			Key:   "operations_workflow_started",
			Title: "",
			Unit:  "rps",
			Query: `sum(rate(workflow_started_total[5m]))`,
			Range: time.Hour,
			Step:  time.Minute,
		},
		{
			Key:   "operations_workflow_errors",
			Title: "",
			Unit:  "rps",
			Query: `sum(rate(workflow_start_error_total[5m]))`,
			Range: time.Hour,
			Step:  time.Minute,
		},
		{
			Key:   "operations_http_error_ratio",
			Title: "",
			Unit:  "ratio",
			Query: `sum(rate(http_requests_total{code=~"5.."}[5m]))` +
				` / clamp_min(sum(rate(http_requests_total[5m])), 1e-9)`,
			Range: time.Hour,
			Step:  time.Minute,
		},
	}
}
