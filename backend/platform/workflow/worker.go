package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"lead-gen/backend/platform/observability/observ"
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
	queue := strings.TrimSpace(opts.TaskQueue)
	if queue == "" {
		queue = c.taskQueue
	}
	inner := worker.New(c.inner, queue, worker.Options{})
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

// TaskQueue возвращает очередь задач воркера.
func (w *Worker) TaskQueue() string { return w.taskQueue }

// Run запускает воркер и блокируется до отмены контекста. Ошибка запуска
// (например, недоступность Temporal) возвращается вызывающему: сервис обязан
// честно сообщить о деградации, а не молча остаться без исполнения.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return ErrNotConfigured
	}
	if w.logger != nil {
		w.logger.Info("workflow: воркер запущен", observ.Fields{}, "task_queue", w.taskQueue)
	}
	// Worker.Run ожидает канал прерывания: закрываем его по отмене контекста.
	interrupt := make(chan interface{})
	go func() {
		<-ctx.Done()
		close(interrupt)
	}()
	if err := w.inner.Run(interrupt); err != nil {
		return fmt.Errorf("workflow: воркер очереди %q: %w", w.taskQueue, err)
	}
	return nil
}
