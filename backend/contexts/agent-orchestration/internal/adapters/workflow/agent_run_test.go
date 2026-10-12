package workflowadapter

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/agent-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/registry"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// countingExecutor — тестовый исполнитель блока, считающий вызовы.
type countingExecutor struct {
	ref   domain.BlockRef
	calls int
}

func (e *countingExecutor) Ref() domain.BlockRef { return e.ref }

func (e *countingExecutor) Execute(context.Context, ports.StepInput) (ports.StepResult, error) {
	e.calls++
	return ports.StepResult{Outcome: domain.Succeeded(), ArtifactRef: "company-1"}, nil
}

// fixedClock — детерминированные часы тестов.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// runHarness — окружение workflow-теста с общим состоянием.
type runHarness struct {
	activities *Activities
	runs       ports.RunRepository
	uow        ports.UnitOfWork
	executor   *countingExecutor
	clock      fixedClock
}

func newRunHarness(t *testing.T) runHarness {
	t.Helper()
	repos := inmemory.New()
	journal := platformoutbox.NewMemoryJournal()
	uow := outboxinmemory.NewUnitOfWork(repos.Store(), journal)
	executor := &countingExecutor{ref: "search"}
	reg, err := registry.New(executor)
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	clock := fixedClock{at: time.Date(2026, time.June, 1, 10, 0, 0, 0, time.UTC)}
	handler := command.NewRunStepHandler(uow, reg, clock, idgen.NewRandom())
	return runHarness{
		activities: &Activities{Steps: handler, Runs: repos.Runs},
		runs:       repos.Runs,
		uow:        uow,
		executor:   executor,
		clock:      clock,
	}
}

func (h runHarness) clockAt() time.Time { return h.clock.at }

// seedRun сохраняет запуск по пайплайну из двух шагов блока search.
func seedRun(t *testing.T, h runHarness, steps int, completed int) {
	t.Helper()
	ctx := context.Background()
	config, err := domain.NewStepConfig(nil, 0)
	if err != nil {
		t.Fatalf("NewStepConfig: %v", err)
	}
	block, err := domain.NewBlock("search", "search", "lead-search", false, true)
	if err != nil {
		t.Fatalf("NewBlock: %v", err)
	}
	blockRegistry, err := domain.NewBlockRegistry(block)
	if err != nil {
		t.Fatalf("NewBlockRegistry: %v", err)
	}
	specs := make([]domain.StepSpec, 0, steps)
	for i := 0; i < steps; i++ {
		specs = append(specs, domain.StepSpec{ID: domain.PipelineStepID(stepID(i)), Ref: "search", Config: config})
	}
	pipeline, err := domain.NewPipeline("pipe-1", "tenant-1", 1, specs, blockRegistry)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := pipeline.Publish(h.clockAt(), domain.EventHeader{EventID: "evt-publish"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	mission, err := domain.NewAgentMission("mission-1", "company-1", "tenant-1", "цель")
	if err != nil {
		t.Fatalf("NewAgentMission: %v", err)
	}
	run, err := domain.NewRun(domain.NewRunParams{
		ID: "run-1", Mission: mission, Pipeline: pipeline, At: h.clockAt(),
		Header: domain.EventHeader{EventID: "evt-run"},
	})
	if err != nil {
		t.Fatalf("NewRun: %v", err)
	}
	if err := run.Start(h.clockAt(), domain.EventHeader{EventID: "evt-start"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < completed; i++ {
		id := domain.PipelineStepID(stepID(i))
		if _, _, err := run.BeginStep(id, h.clockAt()); err != nil {
			t.Fatalf("BeginStep %s: %v", id, err)
		}
		if _, err := run.CompleteStep(id, domain.Succeeded(), "company-1", "", 0, h.clockAt(), domain.EventHeader{EventID: "evt-step"}); err != nil {
			t.Fatalf("CompleteStep %s: %v", id, err)
		}
	}
	if err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Runs().Save(ctx, run, 0)
	}); err != nil {
		t.Fatalf("сохранение запуска: %v", err)
	}
}

func stepID(i int) string { return "step-" + string(rune('a'+i)) }

// TestWorkflowDrivesRunToTerminal проверяет, что workflow проводит запуск по всем
// шагам до терминального состояния.
func TestWorkflowDrivesRunToTerminal(t *testing.T) {
	h := newRunHarness(t)
	seedRun(t, h, 2, 0)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(AgentRun, workflow.RegisterOptions{Name: WorkflowName})
	env.RegisterActivityWithOptions(h.activities.ExecuteStep, activity.RegisterOptions{Name: ActivityExecuteStep})
	env.RegisterActivityWithOptions(h.activities.RunState, activity.RegisterOptions{Name: ActivityRunState})

	env.ExecuteWorkflow(AgentRun, Params{RunID: "run-1", TenantID: "tenant-1"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow вернул ошибку: %v", err)
	}
	if h.executor.calls != 2 {
		t.Fatalf("исполнитель вызван %d раз, ожидалось 2", h.executor.calls)
	}
	run, err := h.runs.FindByID(context.Background(), "run-1", "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if run.Status() != domain.StatusCompleted {
		t.Fatalf("статус = %q, ожидался %q", run.Status(), domain.StatusCompleted)
	}
}

// TestWorkflowContinuesAfterRestart проверяет продолжение после перезапуска: запуск,
// у которого первый шаг уже зафиксирован в хранилище, не переигрывает его — workflow
// доисполняет только оставшийся шаг.
func TestWorkflowContinuesAfterRestart(t *testing.T) {
	h := newRunHarness(t)
	seedRun(t, h, 2, 1) // первый шаг завершён до «перезапуска»

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(AgentRun, workflow.RegisterOptions{Name: WorkflowName})
	env.RegisterActivityWithOptions(h.activities.ExecuteStep, activity.RegisterOptions{Name: ActivityExecuteStep})
	env.RegisterActivityWithOptions(h.activities.RunState, activity.RegisterOptions{Name: ActivityRunState})

	env.ExecuteWorkflow(AgentRun, Params{RunID: "run-1", TenantID: "tenant-1"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow вернул ошибку: %v", err)
	}
	if h.executor.calls != 1 {
		t.Fatalf("исполнитель вызван %d раз, ожидался 1 (первый шаг не переигрывается)", h.executor.calls)
	}
	run, err := h.runs.FindByID(context.Background(), "run-1", "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if run.Status() != domain.StatusCompleted {
		t.Fatalf("статус = %q, ожидался %q", run.Status(), domain.StatusCompleted)
	}
}
