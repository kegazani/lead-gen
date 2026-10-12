package agentorchestration

import (
	"context"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/agent-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/registry"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// stepClock — детерминированные часы тестов прогона шага.
type stepClock struct{ at time.Time }

func (c stepClock) Now() time.Time { return c.at }

// stepExecutor — тестовый исполнитель блока «по контракту владельца».
type stepExecutor struct {
	ref      domain.BlockRef
	result   ports.StepResult
	err      error
	executed int
}

func (e *stepExecutor) Ref() domain.BlockRef { return e.ref }

func (e *stepExecutor) Execute(context.Context, ports.StepInput) (ports.StepResult, error) {
	e.executed++
	return e.result, e.err
}

// stepHarness — собранный сценарий прогона шага с общим состоянием.
type stepHarness struct {
	handler *command.RunStepHandler
	uow     ports.UnitOfWork
	runs    ports.RunRepository
	journal platformoutbox.Journal
}

func newStepHarness(t *testing.T, executors ...ports.BlockExecutor) stepHarness {
	t.Helper()
	repos := inmemory.New()
	journal := platformoutbox.NewMemoryJournal()
	uow := outboxinmemory.NewUnitOfWork(repos.Store(), journal)
	reg, err := registry.New(executors...)
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	clock := stepClock{at: time.Date(2026, time.May, 5, 12, 0, 0, 0, time.UTC)}
	return stepHarness{
		handler: command.NewRunStepHandler(uow, reg, clock, idgen.NewRandom()),
		uow:     uow,
		runs:    repos.Runs,
		journal: journal,
	}
}

// stepSearchBlock собирает обязательный блок поиска.
func stepSearchBlock(t *testing.T) domain.Block {
	t.Helper()
	block, err := domain.NewBlock("search", "search", "lead-search", false, true)
	if err != nil {
		t.Fatalf("NewBlock: %v", err)
	}
	return block
}

// seedRunningRun создаёт сохранённый запуск в состоянии running с одним шагом «search».
func seedRunningRun(t *testing.T, h stepHarness) {
	t.Helper()
	ctx := context.Background()
	config, err := domain.NewStepConfig(nil, 0)
	if err != nil {
		t.Fatalf("NewStepConfig: %v", err)
	}
	blocks, err := domain.NewBlockRegistry(stepSearchBlock(t))
	if err != nil {
		t.Fatalf("NewBlockRegistry: %v", err)
	}
	pipeline, err := domain.NewPipeline("pipe-1", "tenant-1", 1, []domain.StepSpec{{ID: "search", Ref: "search", Config: config}}, blocks)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	at := time.Date(2026, time.May, 5, 11, 0, 0, 0, time.UTC)
	if err := pipeline.Publish(at, domain.EventHeader{EventID: "evt-publish"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	mission, err := domain.NewAgentMission("mission-1", "company-1", "tenant-1", "цель")
	if err != nil {
		t.Fatalf("NewAgentMission: %v", err)
	}
	run, err := domain.NewRun(domain.NewRunParams{
		ID: "run-1", Mission: mission, Pipeline: pipeline, At: at,
		Header: domain.EventHeader{EventID: "evt-run"},
	})
	if err != nil {
		t.Fatalf("NewRun: %v", err)
	}
	if err := run.Start(at, domain.EventHeader{EventID: "evt-start"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Runs().Save(ctx, run, 0)
	}); err != nil {
		t.Fatalf("сохранение запуска: %v", err)
	}
}

func TestRunStepWritesLifecycleEvent(t *testing.T) {
	executor := &stepExecutor{ref: "search", result: ports.StepResult{Outcome: domain.Succeeded(), ArtifactRef: "company-42"}}
	h := newStepHarness(t, executor)
	seedRunningRun(t, h)

	result, err := h.handler.Handle(context.Background(), command.RunStep{RunID: "run-1", TenantID: "tenant-1", CorrelationID: "corr"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Outcome.IsSuccess() {
		t.Fatalf("исход шага = %q, ожидался успех", result.Outcome.Kind())
	}
	if result.RunStatus != domain.StatusCompleted {
		t.Fatalf("статус запуска = %q, ожидался %q", result.RunStatus, domain.StatusCompleted)
	}
	if executor.executed != 1 {
		t.Fatalf("исполнитель вызван %d раз, ожидался 1", executor.executed)
	}

	messages := stepPendingMessages(t, h.journal)
	if len(messages) != 1 {
		t.Fatalf("записей outbox = %d, ожидалась 1 (событие шага)", len(messages))
	}
	message := messages[0]
	if message.Headers["event_name"] != "agent-step-completed" {
		t.Fatalf("имя события = %q", message.Headers["event_name"])
	}
	body := string(message.Body)
	for _, want := range []string{"run-1", "search", "succeeded"} {
		if !strings.Contains(body, want) {
			t.Fatalf("тело события не содержит %q:\n%s", want, body)
		}
	}
}

func TestRunStepMissingExecutorIsHonestUnavailable(t *testing.T) {
	h := newStepHarness(t) // исполнитель блока не зарегистрирован
	seedRunningRun(t, h)

	result, err := h.handler.Handle(context.Background(), command.RunStep{RunID: "run-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
	if result.RunStatus != domain.StatusBlocked || result.RunStatus.IsTerminal() {
		t.Fatalf("статус = %q, ожидался незавершённый %q", result.RunStatus, domain.StatusBlocked)
	}
	body := string(stepPendingMessages(t, h.journal)[0].Body)
	if strings.Contains(body, "succeeded") {
		t.Fatalf("недоступность подменена успехом:\n%s", body)
	}
}

func TestRunStepProviderUnavailableOutcome(t *testing.T) {
	executor := &stepExecutor{ref: "search", result: ports.StepResult{Outcome: domain.Unavailable("провайдер поиска не выбран")}}
	h := newStepHarness(t, executor)
	seedRunningRun(t, h)

	result, err := h.handler.Handle(context.Background(), command.RunStep{RunID: "run-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Outcome.IsUnavailable() || result.Outcome.Reason() != "провайдер поиска не выбран" {
		t.Fatalf("исход = %q (%q)", result.Outcome.Kind(), result.Outcome.Reason())
	}
	if result.RunStatus.IsTerminal() {
		t.Fatalf("недоступность обязательного шага не должна завершать запуск: %q", result.RunStatus)
	}
}

// stepPendingMessages читает ожидающие записи журнала outbox.
func stepPendingMessages(t *testing.T, journal platformoutbox.Journal) []platformoutbox.Message {
	t.Helper()
	messages, err := journal.Pending(context.Background(), 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	return messages
}
