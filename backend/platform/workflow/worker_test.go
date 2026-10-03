package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

// testWorkflow и testActivity — минимальные валидные регистрации для проверки
// сборки воркера без живого сервера.
func testWorkflow(workflow.Context) error { return nil }

func testActivity(context.Context) error { return nil }

// lazyClient собирает клиент platform/workflow поверх lazy-клиента Temporal:
// соединения нет, но регистрация и разрешение очереди задач проверяемы.
func lazyClient(t *testing.T) *Client {
	t.Helper()
	inner, err := client.NewLazyClient(client.Options{HostPort: "127.0.0.1:7233", Namespace: "default"})
	if err != nil {
		t.Fatalf("lazy-клиент Temporal: %v", err)
	}
	return &Client{inner: inner, namespace: "default", taskQueue: "default-queue"}
}

// TestWorkerOptionsDefaults проверяет маппинг нулевых настроек воркера в
// осмысленные значения по умолчанию.
func TestWorkerOptionsDefaults(t *testing.T) {
	opts := WorkerOptions{}.withDefaults()
	if opts.WorkerStopTimeout != defaultWorkerStopTimeout {
		t.Fatalf("WorkerStopTimeout = %v, ожидался %v", opts.WorkerStopTimeout, defaultWorkerStopTimeout)
	}
	if opts.MaxConcurrentActivityExecution != defaultMaxConcurrentActivityExecution {
		t.Fatalf("MaxConcurrentActivityExecution = %d, ожидался %d",
			opts.MaxConcurrentActivityExecution, defaultMaxConcurrentActivityExecution)
	}
	if opts.MaxConcurrentWorkflowTaskExecution != defaultMaxConcurrentWorkflowTaskExecution {
		t.Fatalf("MaxConcurrentWorkflowTaskExecution = %d, ожидался %d",
			opts.MaxConcurrentWorkflowTaskExecution, defaultMaxConcurrentWorkflowTaskExecution)
	}
}

// TestWorkerOptionsKeepsExplicit проверяет, что явно заданные настройки не
// подменяются значениями по умолчанию.
func TestWorkerOptionsKeepsExplicit(t *testing.T) {
	opts := WorkerOptions{
		WorkerStopTimeout:                  5 * time.Second,
		MaxConcurrentActivityExecution:     3,
		MaxConcurrentWorkflowTaskExecution: 4,
	}.withDefaults()
	if opts.WorkerStopTimeout != 5*time.Second {
		t.Fatalf("WorkerStopTimeout = %v, ожидался 5s", opts.WorkerStopTimeout)
	}
	if opts.MaxConcurrentActivityExecution != 3 || opts.MaxConcurrentWorkflowTaskExecution != 4 {
		t.Fatalf("лимиты конкурентности = %d/%d, ожидались 3/4",
			opts.MaxConcurrentActivityExecution, opts.MaxConcurrentWorkflowTaskExecution)
	}
}

// TestNewWorkerRejectsUnconfiguredClient проверяет, что без подключённого клиента
// воркер не собирается: сборка честно сообщает о неподключённом рантайме.
func TestNewWorkerRejectsUnconfiguredClient(t *testing.T) {
	if _, err := NewWorker(nil, NewRegistry(), WorkerOptions{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotConfigured", err)
	}
}

// TestNewWorkerRejectsEmptyRegistry проверяет валидацию реестра: воркер без
// регистраций не имеет смысла и не собирается.
func TestNewWorkerRejectsEmptyRegistry(t *testing.T) {
	_, err := NewWorker(lazyClient(t), NewRegistry(), WorkerOptions{})
	if err == nil {
		t.Fatal("пустой реестр должен быть отвергнут")
	}
}

// TestNewWorkerFallsBackToClientQueue проверяет маппинг настроек в сборку:
// воркер наследует очередь задач клиента, если своя не задана, и принимает
// непустой реестр.
func TestNewWorkerFallsBackToClientQueue(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterWorkflowNamed("test", testWorkflow)
	registry.RegisterActivityNamed("test-activity", testActivity)

	worker, err := NewWorker(lazyClient(t), registry, WorkerOptions{})
	if err != nil {
		t.Fatalf("сборка воркера: %v", err)
	}
	if worker.TaskQueue() != "default-queue" {
		t.Fatalf("очередь воркера = %q, ожидалась default-queue", worker.TaskQueue())
	}
}

// TestFatalHandlerInvokesCallback проверяет, что фатальная ошибка воркера
// наблюдаема: явный обработчик вызывается, а не молчит.
func TestFatalHandlerInvokesCallback(t *testing.T) {
	var captured error
	handler := fatalHandler(WorkerOptions{OnFatalError: func(err error) { captured = err }})
	if handler == nil {
		t.Fatal("обработчик фатальных ошибок не собран")
	}
	sentinel := errors.New("фатальная ошибка воркера")
	handler(sentinel)
	if !errors.Is(captured, sentinel) {
		t.Fatalf("обработчик получил %v, ожидалась %v", captured, sentinel)
	}
}

// TestFatalHandlerNilWithoutHooks проверяет, что без обработчика и логгера
// фатальный хук не создаётся: нечего наблюдать.
func TestFatalHandlerNilWithoutHooks(t *testing.T) {
	if fatalHandler(WorkerOptions{}) != nil {
		t.Fatal("без обработчика и логгера хук не нужен")
	}
}
