package agentorchestration

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/agent-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/supervisor"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// supervisorClock — детерминированные часы тестов супервизора.
type supervisorClock struct{ at time.Time }

func (c supervisorClock) Now() time.Time { return c.at }

// supervisorWorkflow — тестовый двойник рантайма долгих процессов для супервизора.
type supervisorWorkflow struct {
	started []string
	stopped []string
	err     error
}

func (f *supervisorWorkflow) StartRun(_ context.Context, in ports.StartRunInput) (ports.RunHandle, error) {
	if f.err != nil {
		return ports.RunHandle{}, f.err
	}
	f.started = append(f.started, in.RunID)
	return ports.RunHandle{WorkflowID: "wf-" + in.RunID}, nil
}

func (f *supervisorWorkflow) RunState(context.Context, string) (ports.RunState, error) {
	return ports.RunState{}, ports.ErrWorkflowNotFound
}

func (f *supervisorWorkflow) SignalApproval(context.Context, ports.ApprovalDecisionInput) error {
	return nil
}

func (f *supervisorWorkflow) StopRun(_ context.Context, in ports.StopInput) error {
	if f.err != nil {
		return f.err
	}
	f.stopped = append(f.stopped, in.RunID)
	return nil
}

// supervisorHarness — собранный супервизор с общим состоянием.
type supervisorHarness struct {
	supervisor *supervisor.Supervisor
	workflow   *supervisorWorkflow
	runs       ports.RunRepository
	uow        ports.UnitOfWork
}

func newSupervisorHarness(t *testing.T, limit int) supervisorHarness {
	t.Helper()
	repos := inmemory.New()
	uow := outboxinmemory.NewUnitOfWork(repos.Store(), platformoutbox.NewMemoryJournal())
	workflow := &supervisorWorkflow{}
	clock := supervisorClock{at: time.Date(2026, time.April, 1, 8, 0, 0, 0, time.UTC)}
	sup := supervisor.New(uow, repos.Runs, workflow, clock, idgen.NewRandom(), limit)
	return supervisorHarness{supervisor: sup, workflow: workflow, runs: repos.Runs, uow: uow}
}

// newSupervisorRun собирает запуск с одной обязательной проверкой и заданным пределом
// попыток шага.
func newSupervisorRun(t *testing.T, id string, attempts int) *domain.AgentRun {
	t.Helper()
	config, err := domain.NewStepConfig(nil, attempts)
	if err != nil {
		t.Fatalf("NewStepConfig: %v", err)
	}
	block, err := domain.NewBlock("search", "search", "lead-search", false, true)
	if err != nil {
		t.Fatalf("NewBlock: %v", err)
	}
	blocks, err := domain.NewBlockRegistry(block)
	if err != nil {
		t.Fatalf("NewBlockRegistry: %v", err)
	}
	pipeline, err := domain.NewPipeline("pipe-1", "tenant-1", 1, []domain.StepSpec{{ID: "search", Ref: "search", Config: config}}, blocks)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := pipeline.Publish(time.Date(2026, time.April, 1, 7, 0, 0, 0, time.UTC), domain.EventHeader{EventID: "evt-publish"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	mission, err := domain.NewAgentMission("mission-1", "company-1", "tenant-1", "цель")
	if err != nil {
		t.Fatalf("NewAgentMission: %v", err)
	}
	run, err := domain.NewRun(domain.NewRunParams{
		ID:       domain.RunID(id),
		Mission:  mission,
		Pipeline: pipeline,
		At:       time.Date(2026, time.April, 1, 7, 30, 0, 0, time.UTC),
		Header:   domain.EventHeader{EventID: domain.EventID("evt-" + id)},
	})
	if err != nil {
		t.Fatalf("NewRun: %v", err)
	}
	return run
}

func TestSupervisorConcurrencyLimit(t *testing.T) {
	h := newSupervisorHarness(t, 2)
	ctx := context.Background()

	for _, id := range []string{"run-a", "run-b", "run-c"} {
		if err := h.supervisor.Admit(ctx, newSupervisorRun(t, id, 0)); err != nil {
			t.Fatalf("Admit %s: %v", id, err)
		}
	}
	if len(h.workflow.started) != 2 {
		t.Fatalf("запущено агентов = %d, ожидалось 2 (предел)", len(h.workflow.started))
	}
	active, err := h.runs.CountActive(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if active != 2 {
		t.Fatalf("занято слотов = %d, ожидалось 2", active)
	}
	queued, err := h.runs.ListByStatus(ctx, domain.StatusQueued, "tenant-1", 0)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("в очереди = %d, ожидался 1", len(queued))
	}

	if err := h.supervisor.Stop(ctx, "run-a", "tenant-1", "actor-1", "оператор остановил", "corr"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if started, err := h.supervisor.Pump(ctx, "tenant-1"); err != nil || started != 1 {
		t.Fatalf("Pump: started=%d err=%v, ожидалось 1", started, err)
	}
	if len(h.workflow.started) != 3 {
		t.Fatalf("запущено агентов = %d, ожидалось 3", len(h.workflow.started))
	}
	if len(h.workflow.stopped) != 1 || h.workflow.stopped[0] != "run-a" {
		t.Fatalf("остановлено = %v, ожидался run-a", h.workflow.stopped)
	}
}

func TestSupervisorRuntimeUnavailableKeepsQueued(t *testing.T) {
	repos := inmemory.New()
	uow := outboxinmemory.NewUnitOfWork(repos.Store(), platformoutbox.NewMemoryJournal())
	workflow := &supervisorWorkflow{err: ports.ErrWorkflowUnavailable}
	sup := supervisor.New(uow, repos.Runs, workflow, supervisorClock{at: time.Now()}, idgen.NewRandom(), 2)

	ctx := context.Background()
	if err := sup.Admit(ctx, newSupervisorRun(t, "run-a", 0)); err == nil {
		t.Fatal("недоступность рантайма должна быть ошибкой, а не успешным запуском")
	}
	queued, err := repos.Runs.ListByStatus(ctx, domain.StatusQueued, "tenant-1", 0)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("агент не остался в очереди: %d", len(queued))
	}
}

func TestSupervisorRetryBoundedByAttempts(t *testing.T) {
	h := newSupervisorHarness(t, 2)
	ctx := context.Background()
	if err := h.supervisor.Admit(ctx, newSupervisorRun(t, "run-a", 2)); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	fresh, err := h.runs.FindByID(ctx, "run-a", "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	revision := fresh.Revision()
	if _, _, err := fresh.BeginStep("search", time.Now()); err != nil {
		t.Fatalf("BeginStep: %v", err)
	}
	retryable, err := fresh.FailStep("search", "сбой источника", time.Now(), domain.EventHeader{EventID: "evt-fail-1"})
	if err != nil {
		t.Fatalf("FailStep: %v", err)
	}
	if !retryable {
		t.Fatal("первый отказ при двух попытках должен быть повторяемым")
	}
	if err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Runs().Save(ctx, fresh, revision)
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if ok, err := h.supervisor.RetryStep(ctx, "run-a", "tenant-1"); err != nil || !ok {
		t.Fatalf("RetryStep = %v, ожидался true (err=%v)", ok, err)
	}

	fresh2, err := h.runs.FindByID(ctx, "run-a", "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	revision2 := fresh2.Revision()
	if _, _, err := fresh2.BeginStep("search", time.Now()); err != nil {
		t.Fatalf("BeginStep: %v", err)
	}
	retryable, err = fresh2.FailStep("search", "сбой источника", time.Now(), domain.EventHeader{EventID: "evt-fail-2"})
	if err != nil {
		t.Fatalf("FailStep: %v", err)
	}
	if retryable {
		t.Fatal("второй отказ при двух попытках должен исчерпать попытки")
	}
	if err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Runs().Save(ctx, fresh2, revision2)
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if ok, err := h.supervisor.RetryStep(ctx, "run-a", "tenant-1"); err != nil || ok {
		t.Fatalf("RetryStep = %v, ожидался false (err=%v)", ok, err)
	}
}
