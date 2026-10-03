package workflowadapter

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/api/serviceerror"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformworkflow "lead-gen/backend/platform/workflow"
)

// WorkflowIDPrefix — префикс идентификатора исполнения, выведенного из заявки.
const WorkflowIDPrefix = "fulfillment-"

// WorkflowID возвращает идентификатор исполнения для заявки. Он же — ключ
// идемпотентности: повторный запуск с тем же id не создаёт второго исполнения.
func WorkflowID(requestID string) string { return WorkflowIDPrefix + requestID }

// Client — адаптер портов запуска и чтения состояния workflow поверх
// platform/workflow. Контекст зависит от портов, а не от Temporal напрямую.
type Client struct {
	client    *platformworkflow.Client
	taskQueue string
	policy    ExecutionPolicy
}

// NewClient собирает адаптер из клиента platform/workflow. nil-клиент допустим:
// тогда порты честно отвечают «рантайм недоступен». policy задаёт таймауты и
// сроки исполнения (нулевые поля заполняются значениями по умолчанию).
func NewClient(client *platformworkflow.Client, taskQueue string, policy ExecutionPolicy) *Client {
	return &Client{client: client, taskQueue: taskQueue, policy: policy}
}

// Register регистрирует workflow и activities фулфилмента в реестре воркера.
func Register(registry *platformworkflow.Registry, activities *Activities) {
	registry.RegisterWorkflowNamed(WorkflowName, Fulfillment)
	registry.RegisterActivityNamed(ActivityStartProcessing, activities.HandleStartProcessing)
	registry.RegisterActivityNamed(ActivityFulfillRequest, activities.HandleFulfillRequest)
	registry.RegisterActivityNamed(ActivityCancelRequest, activities.HandleCancelRequest)
	registry.RegisterActivityNamed(ActivitySearchLeads, activities.SearchLeads)
	registry.RegisterActivityNamed(ActivityDraftEmails, activities.DraftEmails)
}

// StartFulfillment запускает workflow исполнения заявки. Реализует
// ports.WorkflowStarter. При недоступности рантайма возвращает
// ports.ErrWorkflowUnavailable: заявка при этом остаётся в текущем статусе.
// Повторный запуск для той же заявки идемпотентен: возвращается ссылка на
// существующее исполнение с признаком AlreadyStarted, а не ошибка.
func (c *Client) StartFulfillment(ctx context.Context, in ports.StartFulfillment) (ports.WorkflowRun, error) {
	if c == nil || c.client == nil {
		return ports.WorkflowRun{}, ports.ErrWorkflowUnavailable
	}
	policy := c.policy.withDefaults()
	run, err := c.client.Start(ctx, platformworkflow.StartOptions{
		WorkflowID:       WorkflowID(in.RequestID),
		TaskQueue:        c.taskQueue,
		ExecutionTimeout: policy.ExecutionTimeout,
	}, Fulfillment, Params{
		RequestID:     in.RequestID,
		TenantID:      in.TenantID,
		CorrelationID: in.CorrelationID,
		LeadTarget:    in.LeadTarget,
		Policy:        policy,
	})
	if err != nil {
		return ports.WorkflowRun{}, fmt.Errorf("%w: %v", ports.ErrWorkflowUnavailable, err)
	}
	return ports.WorkflowRun{WorkflowID: run.WorkflowID, RunID: run.RunID, AlreadyStarted: run.AlreadyStarted}, nil
}

// WorkflowStatus читает наблюдаемое состояние исполнения заявки. Реализует
// ports.WorkflowStatusReader: состояние бегущего workflow видно в API и, через
// него, в консоли.
func (c *Client) WorkflowStatus(ctx context.Context, requestID string) (ports.WorkflowStatus, error) {
	if c == nil || c.client == nil {
		return ports.WorkflowStatus{}, ports.ErrWorkflowUnavailable
	}
	workflowID := WorkflowID(requestID)
	var status Status
	if err := c.client.Query(ctx, workflowID, "", QueryStatus, &status); err != nil {
		if isNotFound(err) {
			return ports.WorkflowStatus{}, ports.ErrWorkflowNotFound
		}
		return ports.WorkflowStatus{}, fmt.Errorf("%w: %v", ports.ErrWorkflowUnavailable, err)
	}
	result := ports.WorkflowStatus{
		RequestID:  requestID,
		WorkflowID: workflowID,
		Phase:      status.Phase,
		Found:      status.Found,
		Drafted:    status.Drafted,
		NoLeads:    status.NoLeads,
	}
	if execution, err := c.client.Describe(ctx, workflowID, ""); err == nil {
		result.Execution = execution.Status
		result.RunID = execution.RunID
	}
	return result, nil
}

// isNotFound сообщает, что ошибка Temporal означает отсутствие исполнения.
func isNotFound(err error) bool {
	var notFound *serviceerror.NotFound
	return errors.As(err, &notFound)
}

// SignalApprovalDecision подаёт сигнал утверждения результата бегущему исполнению.
// Реализует ports.WorkflowSignaler.
func (c *Client) SignalApprovalDecision(ctx context.Context, in ports.WorkflowDecision) error {
	return c.signal(ctx, in.RequestID, SignalApprovalDecision, ApprovalDecision{Approved: in.Approved, Actor: in.Actor})
}

// signal подаёт сигнал по идентификатору заявки, переводя ошибки Temporal в
// исходы порта.
func (c *Client) signal(ctx context.Context, requestID, name string, value any) error {
	if c == nil || c.client == nil {
		return ports.ErrWorkflowUnavailable
	}
	if err := c.client.Signal(ctx, WorkflowID(requestID), "", name, value); err != nil {
		if isNotFound(err) {
			return ports.ErrWorkflowNotFound
		}
		return fmt.Errorf("%w: %v", ports.ErrWorkflowUnavailable, err)
	}
	return nil
}

// Проверка соответствия портам на этапе компиляции.
var (
	_ ports.WorkflowStarter      = (*Client)(nil)
	_ ports.WorkflowStatusReader = (*Client)(nil)
	_ ports.WorkflowSignaler     = (*Client)(nil)
)
