package workflow

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"

	"lead-gen/backend/platform/observability/observ"
)

// ErrNotConfigured — рантайм Temporal не сконфигурирован (пустой адрес).
var ErrNotConfigured = errors.New("workflow: рантайм Temporal не сконфигурирован")

// Имена метрик запуска workflow. Совпадают с каталогом prommetrics.specs; имя
// вне каталога молча отбрасывается адаптером, поэтому имена здесь — часть
// контракта, а не произвольные строки. Меток у счётчиков нет намеренно:
// идентификатор исполнения (workflow_id) высококардинален и в метки не
// допускается — его место в логах через observ.Fields.WorkflowID.
const (
	metricWorkflowStarted             = "workflow_started_total"
	metricWorkflowStartError          = "workflow_start_error_total"
	metricWorkflowStartAlreadyStarted = "workflow_start_already_started_total"
)

// Client — обёртка клиента Temporal: соединение, namespace и очередь задач по
// умолчанию. Держит метрики сервиса и увеличивает счётчики запусков, чтобы
// состояние исполнения было видно в существующем контуре наблюдаемости.
type Client struct {
	inner     client.Client
	namespace string
	taskQueue string
	metrics   observ.Metrics
}

// Dial подключается к Temporal и возвращает клиента. Ошибка подключения — отказ
// вызывающему: сервис обязан деградировать явно, а не делать вид, что рантайм
// доступен. Пустой Address возвращает ErrNotConfigured.
func Dial(ctx context.Context, cfg Config, metrics observ.Metrics) (*Client, error) {
	cfg = cfg.WithDefaults()
	if !cfg.Enabled() {
		return nil, ErrNotConfigured
	}
	options := client.Options{
		HostPort:  cfg.Address,
		Namespace: cfg.Namespace,
		Identity:  cfg.Identity,
	}
	if cfg.Cloud() {
		// Temporal Cloud: ключ доступа как credentials поверх TLS.
		options.Credentials = client.NewAPIKeyStaticCredentials(cfg.APIKey)
		options.ConnectionOptions = client.ConnectionOptions{TLS: &tls.Config{MinVersion: tls.VersionTLS12}}
	}
	inner, err := client.DialContext(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("workflow: подключение к Temporal %q: %w", cfg.Address, err)
	}
	return &Client{inner: inner, namespace: cfg.Namespace, taskQueue: cfg.TaskQueue, metrics: metrics}, nil
}

// CheckHealth проверяет соединение с Temporal: доступен ли рантайм прямо сейчас.
// Используется фоновым монитором доступности и, через признак готовности, пробой
// /readyz — доступность отражает фактическое состояние, а не отметку при старте.
func (c *Client) CheckHealth(ctx context.Context) error {
	if c == nil || c.inner == nil {
		return ErrNotConfigured
	}
	if _, err := c.inner.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		return fmt.Errorf("workflow: проверка здоровья Temporal: %w", err)
	}
	return nil
}

// Namespace возвращает пространство имён клиента.
func (c *Client) Namespace() string { return c.namespace }

// TaskQueue возвращает очередь задач по умолчанию.
func (c *Client) TaskQueue() string { return c.taskQueue }

// Close закрывает соединение. Повторный вызов безопасен.
func (c *Client) Close() {
	if c == nil || c.inner == nil {
		return
	}
	c.inner.Close()
	c.inner = nil
}

// StartOptions — параметры запуска исполнения.
type StartOptions struct {
	// WorkflowID — идентификатор исполнения. Обязателен: он же ключ
	// идемпотентности запуска (повторный старт с тем же id не создаёт второго
	// исполнения).
	WorkflowID string

	// TaskQueue — очередь задач. Пусто — очередь клиента по умолчанию.
	TaskQueue string

	// ExecutionTimeout — предельное время исполнения. Ноль — без предела (не
	// рекомендуется для заявок с человеческой паузой: исполнение не должно жить
	// вечно).
	ExecutionTimeout time.Duration

	// WorkflowIDReusePolicy — политика переиспользования идентификатора
	// исполнения после его завершения. Ноль — политика Temporal по умолчанию
	// (AllowDuplicate). Для заявок, допускающих повторный запуск упавшего
	// исполнения с тем же идентификатором, задаётся ALLOW_DUPLICATE_FAILED_ONLY:
	// reuse разрешён только после неудачного close-статуса.
	WorkflowIDReusePolicy enumspb.WorkflowIdReusePolicy
}

// Run — ссылка на запущенное исполнение. AlreadyStarted отличает повторный старт
// уже существующего исполнения от только что созданного.
type Run struct {
	WorkflowID     string
	RunID          string
	AlreadyStarted bool
}

// Start запускает workflow. Аргументы (args) — payload уровня идентификаторов.
//
// Повторный старт с тем же WorkflowID трактуется идемпотентно: Temporal отвечает
// WorkflowExecutionAlreadyStarted, и клиент возвращает ссылку на существующее
// исполнение с признаком AlreadyStarted, а не ошибку. Ошибка остаётся только для
// настоящей недоступности рантайма.
func (c *Client) Start(ctx context.Context, opts StartOptions, workflowFn any, args ...any) (Run, error) {
	if c == nil || c.inner == nil {
		return Run{}, ErrNotConfigured
	}
	queue := opts.TaskQueue
	if queue == "" {
		queue = c.taskQueue
	}
	run, err := c.inner.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       opts.WorkflowID,
		TaskQueue:                queue,
		WorkflowExecutionTimeout: opts.ExecutionTimeout,
		WorkflowIDReusePolicy:    opts.WorkflowIDReusePolicy,
	}, workflowFn, args...)
	if err != nil {
		if existing, ok := startedRun(err, opts.WorkflowID); ok {
			c.count(metricWorkflowStartAlreadyStarted, nil)
			return existing, nil
		}
		c.count(metricWorkflowStartError, nil)
		return Run{}, fmt.Errorf("workflow: запуск %q: %w", opts.WorkflowID, err)
	}
	c.count(metricWorkflowStarted, nil)
	return Run{WorkflowID: run.GetID(), RunID: run.GetRunID()}, nil
}

// startedRun распознаёт повторный старт исполнения. Temporal сообщает
// WorkflowExecutionAlreadyStarted с run id существующего исполнения; это не сбой,
// поэтому клиент возвращает ссылку на него и признак «уже запущено».
func startedRun(err error, workflowID string) (Run, bool) {
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if !errors.As(err, &alreadyStarted) {
		return Run{}, false
	}
	return Run{WorkflowID: workflowID, RunID: alreadyStarted.RunId, AlreadyStarted: true}, true
}

// Execution — наблюдаемое состояние исполнения workflow для консоли и API.
type Execution struct {
	WorkflowID string
	RunID      string
	Status     string
	StartTime  time.Time
	CloseTime  time.Time
	Activities []ActivityState
}

// ActivityState содержит только эксплуатационные метаданные шага, без payload
// и свободного текста исключений внешних интеграций.
type ActivityState struct {
	Type          string
	State         string
	Attempt       int32
	StartedAt     time.Time
	HeartbeatAt   time.Time
	NextAttemptAt time.Time
	FailureKind   string
}

func activityState(info *workflowpb.PendingActivityInfo) ActivityState {
	state := ActivityState{Type: info.GetActivityType().GetName(), State: info.GetState().String(), Attempt: info.GetAttempt()}
	if t := info.GetLastStartedTime(); t != nil {
		state.StartedAt = t.AsTime()
	}
	if t := info.GetLastHeartbeatTime(); t != nil {
		state.HeartbeatAt = t.AsTime()
	}
	if t := info.GetNextAttemptScheduleTime(); t != nil {
		state.NextAttemptAt = t.AsTime()
	}
	if failure := info.GetLastFailure(); failure != nil {
		state.FailureKind = "error"
		if failure.GetTimeoutFailureInfo() != nil {
			state.FailureKind = "timeout"
		} else if failure.GetCanceledFailureInfo() != nil {
			state.FailureKind = "cancelled"
		}
	}
	return state
}

// Describe возвращает состояние исполнения. Пустой runID означает текущее
// (последнее) исполнение с этим workflow id.
func (c *Client) Describe(ctx context.Context, workflowID, runID string) (Execution, error) {
	if c == nil || c.inner == nil {
		return Execution{}, ErrNotConfigured
	}
	response, err := c.inner.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return Execution{}, fmt.Errorf("workflow: описание %q: %w", workflowID, err)
	}
	info := response.GetWorkflowExecutionInfo()
	execution := Execution{
		WorkflowID: info.GetExecution().GetWorkflowId(),
		RunID:      info.GetExecution().GetRunId(),
		Status:     info.GetStatus().String(),
	}
	if info.GetStartTime() != nil {
		execution.StartTime = info.GetStartTime().AsTime()
	}
	if info.GetCloseTime() != nil {
		execution.CloseTime = info.GetCloseTime().AsTime()
	}
	for _, pending := range response.GetPendingActivities() {
		if pending != nil {
			execution.Activities = append(execution.Activities, activityState(pending))
		}
	}
	return execution, nil
}

// Signal подаёт сигнал бегущему исполнению. Пустой runID означает текущее
// (последнее) исполнение. value сериализуется дата-конвертером Temporal; по
// правилу payload-гигиены это должны быть только идентификаторы и коды.
func (c *Client) Signal(ctx context.Context, workflowID, runID, signalName string, value any) error {
	if c == nil || c.inner == nil {
		return ErrNotConfigured
	}
	if err := c.inner.SignalWorkflow(ctx, workflowID, runID, signalName, value); err != nil {
		return fmt.Errorf("workflow: сигнал %q к %q: %w", signalName, workflowID, err)
	}
	return nil
}

// Cancel запрашивает отмену текущего исполнения. Подтверждение означает, что
// Temporal принял команду отмены, а не то, что уже выполняющийся внешний эффект
// удалось отозвать.
func (c *Client) Cancel(ctx context.Context, workflowID, runID string) error {
	if c == nil || c.inner == nil {
		return ErrNotConfigured
	}
	if err := c.inner.CancelWorkflow(ctx, workflowID, runID); err != nil {
		return fmt.Errorf("workflow: запрос отмены %q: %w", workflowID, err)
	}
	return nil
}

// Query отправляет запрос (query) бегущему исполнению и разбирает ответ в
// result. Пустой runID означает текущее (последнее) исполнение.
func (c *Client) Query(ctx context.Context, workflowID, runID, queryType string, result any) error {
	if c == nil || c.inner == nil {
		return ErrNotConfigured
	}
	encoded, err := c.inner.QueryWorkflow(ctx, workflowID, runID, queryType)
	if err != nil {
		return fmt.Errorf("workflow: запрос %q к %q: %w", queryType, workflowID, err)
	}
	if result == nil {
		return nil
	}
	if err := encoded.Get(result); err != nil {
		return fmt.Errorf("workflow: разбор ответа %q: %w", queryType, err)
	}
	return nil
}

// count увеличивает счётчик метрик, если приёмник задан.
func (c *Client) count(name string, labels map[string]string) {
	if c == nil || c.metrics == nil {
		return
	}
	c.metrics.Incr(name, labels)
}
