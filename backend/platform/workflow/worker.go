package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"lead-gen/backend/platform/observability/observ"
)

// Значения устойчивости воркера по умолчанию.
const (
	// defaultWorkerStopTimeout — время, отведённое воркеру на завершение уже
	// взятых задач при остановке (graceful drain).
	defaultWorkerStopTimeout = 30 * time.Second

	// defaultMaxConcurrentActivityExecution — предел одновременных activities.
	defaultMaxConcurrentActivityExecution = 10

	// defaultMaxConcurrentWorkflowTaskExecution — предел одновременных задач
	// workflow.
	defaultMaxConcurrentWorkflowTaskExecution = 10
)

// Worker — host воркера Temporal: опрашивает очередь задач и исполняет
// зарегистрированные workflow/activities сервиса.
type Worker struct {
	inner     worker.Worker
	taskQueue string
	logger    observ.Logger
}

// WorkerOptions — параметры воркера.
type WorkerOptions struct {
	// TaskQueue — очередь задач. Пусто — очередь по умолчанию у клиента.
	TaskQueue string

	// Logger — логгер сервиса. nil — без логирования старта/остановки.
	Logger observ.Logger

	// WorkerStopTimeout — сколько воркер ждёт завершения уже взятых задач при
	// остановке. Ноль — осмысленное значение по умолчанию.
	WorkerStopTimeout time.Duration

	// MaxConcurrentActivityExecution — предел одновременных activities. Ноль —
	// значение по умолчанию.
	MaxConcurrentActivityExecution int

	// MaxConcurrentWorkflowTaskExecution — предел одновременных задач workflow.
	// Ноль — значение по умолчанию.
	MaxConcurrentWorkflowTaskExecution int

	// OnFatalError — обработчик фатальных ошибок воркера. nil — ошибка
	// логируется через Logger: сбой наблюдаем, а не теряется молча.
	OnFatalError func(error)
}

// withDefaults заполняет нулевые поля значениями по умолчанию.
func (o WorkerOptions) withDefaults() WorkerOptions {
	if o.WorkerStopTimeout <= 0 {
		o.WorkerStopTimeout = defaultWorkerStopTimeout
	}
	if o.MaxConcurrentActivityExecution <= 0 {
		o.MaxConcurrentActivityExecution = defaultMaxConcurrentActivityExecution
	}
	if o.MaxConcurrentWorkflowTaskExecution <= 0 {
		o.MaxConcurrentWorkflowTaskExecution = defaultMaxConcurrentWorkflowTaskExecution
	}
	return o
}

// NewWorker собирает воркер из клиента и реестра. Регистрация выполняется один
// раз при сборке: воркер исполняет ровно то, что зарегистрировал его сервис.
// Пустой реестр — ошибка: воркер без регистраций не имеет смысла.
func NewWorker(c *Client, registry *Registry, opts WorkerOptions) (*Worker, error) {
	if c == nil || c.inner == nil {
		return nil, ErrNotConfigured
	}
	if registry == nil || registry.Empty() {
		return nil, errors.New("workflow: реестр пуст, воркер нечего исполнять")
	}
	opts = opts.withDefaults()
	queue := strings.TrimSpace(opts.TaskQueue)
	if queue == "" {
		queue = c.taskQueue
	}
	inner := worker.New(c.inner, queue, worker.Options{
		WorkerStopTimeout:                      opts.WorkerStopTimeout,
		MaxConcurrentActivityExecutionSize:     opts.MaxConcurrentActivityExecution,
		MaxConcurrentWorkflowTaskExecutionSize: opts.MaxConcurrentWorkflowTaskExecution,
		OnFatalError:                           fatalHandler(opts),
	})
	for _, registration := range registry.Workflows() {
		if registration.Name == "" {
			inner.RegisterWorkflow(registration.Fn)
			continue
		}
		inner.RegisterWorkflowWithOptions(registration.Fn, workflow.RegisterOptions{Name: registration.Name})
	}
	for _, registration := range registry.Activities() {
		if registration.Name == "" {
			inner.RegisterActivity(registration.Fn)
			continue
		}
		inner.RegisterActivityWithOptions(registration.Fn, activity.RegisterOptions{Name: registration.Name})
	}
	return &Worker{inner: inner, taskQueue: queue, logger: opts.Logger}, nil
}

// fatalHandler собирает обработчик фатальных ошибок воркера: явный обработчик
// вызывающего, а поверх него — логирование через логгер сервиса.
func fatalHandler(opts WorkerOptions) func(error) {
	if opts.OnFatalError == nil && opts.Logger == nil {
		return nil
	}
	return func(err error) {
		if opts.Logger != nil {
			opts.Logger.Error("workflow: фатальная ошибка воркера", err, observ.Fields{}, "task_queue", opts.TaskQueue)
		}
		if opts.OnFatalError != nil {
			opts.OnFatalError(err)
		}
	}
}

// TaskQueue возвращает очередь задач воркера.
func (w *Worker) TaskQueue() string { return w.taskQueue }

// Run запускает воркер и блокируется до отмены контекста. Ошибка запуска
// (например, недоступность Temporal) возвращается вызывающему: сервис обязан
// честно сообщить о деградации, а не молча остаться без исполнения.
//
// По отмене контекста воркер перестаёт брать новые задачи, но дожидается
// завершения уже взятых в пределах WorkerStopTimeout (graceful drain): взятую
// задачу нельзя обрывать немедленно.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return ErrNotConfigured
	}
	if w.logger != nil {
		w.logger.Info("workflow: воркер запущен", observ.Fields{}, "task_queue", w.taskQueue)
	}
	// Worker.Run ожидает канал прерывания: закрываем его по отмене контекста.
	// Внутри Run закрытие канала инициирует Stop, который и выполняет drain.
	interrupt := make(chan interface{})
	go func() {
		<-ctx.Done()
		close(interrupt)
	}()
	if err := w.inner.Run(interrupt); err != nil {
		return fmt.Errorf("workflow: воркер очереди %q: %w", w.taskQueue, err)
	}
	if w.logger != nil {
		w.logger.Info("workflow: воркер остановлен", observ.Fields{}, "task_queue", w.taskQueue)
	}
	return nil
}
