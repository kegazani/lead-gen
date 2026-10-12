package workflowadapter

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/api/serviceerror"

	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformworkflow "lead-gen/backend/platform/workflow"
)

// WorkflowIDPrefix — префикс идентификатора исполнения, выведенного из запуска.
const WorkflowIDPrefix = "agent-run-"

// WorkflowID возвращает идентификатор исполнения для запуска. Он же — ключ
// идемпотентности: повторный старт с тем же id не создаёт второго исполнения.
func WorkflowID(runID string) string { return WorkflowIDPrefix + runID }

// Client — адаптер порта долгих процессов поверх platform/workflow. Контекст
// зависит от порта, а не от Temporal напрямую.
type Client struct {
	client    *platformworkflow.Client
	taskQueue string
	policy    ExecutionPolicy
}

// NewClient собирает адаптер из клиента platform/workflow. nil-клиент допустим:
// тогда порт честно отвечает «рантайм недоступен».
func NewClient(client *platformworkflow.Client, taskQueue string, policy ExecutionPolicy) *Client {
	return &Client{client: client, taskQueue: taskQueue, policy: policy}
}

// Register регистрирует workflow и activities запуска агента в реестре воркера.
func Register(registry *platformworkflow.Registry, activities *Activities) {
	registry.RegisterWorkflowNamed(WorkflowName, AgentRun)
	registry.RegisterActivityNamed(ActivityExecuteStep, activities.ExecuteStep)
	registry.RegisterActivityNamed(ActivityRunState, activities.RunState)
}

// StartRun запускает durable-исполнение запуска. Реализует ports.WorkflowRunner.
// При недоступности рантайма возвращает ports.ErrWorkflowUnavailable: запуск
// остаётся в очереди. Повтор для того же запуска идемпотентен.
func (c *Client) StartRun(ctx context.Context, in ports.StartRunInput) (ports.RunHandle, error) {
	if c == nil || c.client == nil {
		return ports.RunHandle{}, ports.ErrWorkflowUnavailable
	}
	policy := c.policy.withDefaults()
	run, err := c.client.Start(ctx, platformworkflow.StartOptions{
		WorkflowID:       WorkflowID(in.RunID),
		TaskQueue:        c.taskQueue,
		ExecutionTimeout: policy.ExecutionTimeout,
	}, AgentRun, Params{
		RunID:           in.RunID,
		TenantID:        in.TenantID,
		MissionID:       in.MissionID,
		CompanyID:       in.CompanyID,
		PipelineID:      in.PipelineID,
		PipelineVersion: in.PipelineVersion,
		Criteria:        in.Criteria,
		CorrelationID:   in.CorrelationID,
		Policy:          policy,
	})
	if err != nil {
		return ports.RunHandle{}, fmt.Errorf("%w: %v", ports.ErrWorkflowUnavailable, err)
	}
	return ports.RunHandle{WorkflowID: run.WorkflowID, RunID: run.RunID, AlreadyStarted: run.AlreadyStarted}, nil
}

// RunState читает наблюдаемое состояние исполнения запуска. Реализует
// ports.WorkflowRunner.
func (c *Client) RunState(ctx context.Context, runID string) (ports.RunState, error) {
	if c == nil || c.client == nil {
		return ports.RunState{}, ports.ErrWorkflowUnavailable
	}
	workflowID := WorkflowID(runID)
	var status Status
	if err := c.client.Query(ctx, workflowID, "", QueryStatus, &status); err != nil {
		if isNotFound(err) {
			return ports.RunState{}, ports.ErrWorkflowNotFound
		}
		return ports.RunState{}, fmt.Errorf("%w: %v", ports.ErrWorkflowUnavailable, err)
	}
	result := ports.RunState{
		RunID:       runID,
		WorkflowID:  workflowID,
		Phase:       status.Phase,
		CurrentStep: status.CurrentStep,
		Cursor:      status.Cursor,
	}
	if execution, err := c.client.Describe(ctx, workflowID, ""); err == nil {
		result.Execution = execution.Status
	}
	return result, nil
}

// SignalApproval подаёт решение оператора бегущему исполнению. Реализует
// ports.WorkflowRunner.
func (c *Client) SignalApproval(ctx context.Context, in ports.ApprovalDecisionInput) error {
	return c.signal(ctx, in.RunID, SignalApprovalDecision, ApprovalDecision{
		Approved:     in.Approved,
		DraftRef:     in.DraftRef,
		DraftVersion: in.DraftVersion,
		Actor:        in.Actor,
	})
}

// StopRun подаёт команду остановки бегущему исполнению. Реализует
// ports.WorkflowRunner.
func (c *Client) StopRun(ctx context.Context, in ports.StopInput) error {
	return c.signal(ctx, in.RunID, SignalStopRun, StopSignal{Actor: in.Actor, Reason: in.Reason})
}

// signal подаёт сигнал по идентификатору запуска, переводя ошибки Temporal в исходы
// порта.
func (c *Client) signal(ctx context.Context, runID, name string, value any) error {
	if c == nil || c.client == nil {
		return ports.ErrWorkflowUnavailable
	}
	if err := c.client.Signal(ctx, WorkflowID(runID), "", name, value); err != nil {
		if isNotFound(err) {
			return ports.ErrWorkflowNotFound
		}
		return fmt.Errorf("%w: %v", ports.ErrWorkflowUnavailable, err)
	}
	return nil
}

// isNotFound сообщает, что ошибка Temporal означает отсутствие исполнения.
func isNotFound(err error) bool {
	var notFound *serviceerror.NotFound
	return errors.As(err, &notFound)
}

// Проверка соответствия порту долгих процессов на этапе компиляции.
var _ ports.WorkflowRunner = (*Client)(nil)
