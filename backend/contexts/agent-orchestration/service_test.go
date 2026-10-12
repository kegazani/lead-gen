package agentorchestration

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// fakeWorkflow — тестовый двойник рантайма долгих процессов для dev-прогона.
type fakeWorkflow struct{}

func (fakeWorkflow) StartRun(_ context.Context, in ports.StartRunInput) (ports.RunHandle, error) {
	return ports.RunHandle{WorkflowID: "wf-" + in.RunID}, nil
}
func (fakeWorkflow) RunState(context.Context, string) (ports.RunState, error) {
	return ports.RunState{}, ports.ErrWorkflowNotFound
}
func (fakeWorkflow) SignalApproval(context.Context, ports.ApprovalDecisionInput) error { return nil }
func (fakeWorkflow) StopRun(context.Context, ports.StopInput) error                    { return nil }

// testConfig — минимальная конфигурация сборки сервиса.
func testConfig() Config {
	return Config{
		Logger:    observ.NewLogger(),
		Metrics:   observ.NewMetrics(),
		Tracer:    observ.NewTracer(ServiceID),
		Workflows: fakeWorkflow{},
	}
}

func TestServiceBuildsAndExposesRoles(t *testing.T) {
	service, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if service.ID() != ServiceID {
		t.Fatalf("ID = %q, ожидался %q", service.ID(), ServiceID)
	}
	for _, role := range []composition.Role{
		composition.RoleAPI, composition.RoleWorker, composition.RoleChains, composition.RoleSchedule,
	} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %q не объявлена сервисом", role)
		}
	}
}

func TestServiceRequiresObservability(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("сборка без логгера/метрик/трейсера должна быть отвергнута")
	}
}

// TestDevContourPublishesAndStartsAgent проверяет dev-контур: публикация пайплайна,
// запуск агента и честная недоступность шага без исполнителя.
func TestDevContourPublishesAndStartsAgent(t *testing.T) {
	ctx := context.Background()
	block, err := domain.NewBlock("search", "search", "lead-search", false, true)
	if err != nil {
		t.Fatalf("NewBlock: %v", err)
	}
	blocks, err := domain.NewBlockRegistry(block)
	if err != nil {
		t.Fatalf("NewBlockRegistry: %v", err)
	}
	cfg := testConfig()
	cfg.Blocks = blocks
	slice, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := slice.PublishPipeline.Handle(ctx, command.PublishPipeline{
		PipelineID: "pipe-1", TenantID: "tenant-1",
		Steps: []command.PipelineStepInput{{ID: "search", BlockRef: "search"}},
	}); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}

	started, err := slice.StartRun.Handle(ctx, command.StartRun{
		TenantID: "tenant-1", MissionID: "mission-1", CompanyID: "company-1", PipelineID: "pipe-1",
	})
	if err != nil {
		t.Fatalf("запуск агента: %v", err)
	}
	if started.Status != domain.StatusRunning {
		t.Fatalf("статус после запуска = %q, ожидался %q", started.Status, domain.StatusRunning)
	}

	// Исполнитель блока не подключён: шаг честно недоступен, запуск не завершается.
	result, err := slice.RunStep.Handle(ctx, command.RunStep{RunID: started.RunID, TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("прогон шага: %v", err)
	}
	if !result.Outcome.IsUnavailable() || result.RunStatus.IsTerminal() {
		t.Fatalf("недоступность обязательного шага: outcome=%q status=%q", result.Outcome.Kind(), result.RunStatus)
	}
}

// TestWorkerRoleHonestWithoutRuntime проверяет, что роль worker без подключённого
// рантайма и публикации честно сообщает, что ничего не собрано, а не делает вид, что
// работает.
func TestWorkerRoleHonestWithoutRuntime(t *testing.T) {
	service, err := New(Config{
		Logger:  observ.NewLogger(),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("роль worker не объявлена")
	}
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("worker без контуров должен сообщить об ошибке сборки")
	}
}

// TestScheduleRoleRuns проверяет, что роль schedule поднимается в dev-контуре и
// завершается по отмене контекста.
func TestScheduleRoleRuns(t *testing.T) {
	cfg := testConfig()
	cfg.ScheduleTenants = []string{"tenant-1"}
	cfg.ScheduleInterval = 5 * time.Millisecond
	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runner, _ := service.RoleRunner(composition.RoleSchedule)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("расписание завершилось ошибкой: %v", err)
	}
}
