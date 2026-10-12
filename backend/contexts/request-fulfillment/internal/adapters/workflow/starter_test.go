package workflowadapter

import (
	"testing"

	"go.temporal.io/api/enums/v1"

	platformworkflow "lead-gen/backend/platform/workflow"
)

// TestStartOptionsReusePolicy проверяет, что запуск исполнения заявки задаёт
// WorkflowIDReusePolicy = ALLOW_DUPLICATE_FAILED_ONLY и выводит WorkflowID из
// заявки. Именно эта политика вкупе с close-статусом Failed (failRequest
// возвращает non-retryable ошибку) позволяет повторно запустить упавшую заявку с
// тем же идентификатором исполнения (re-run), тогда как успешно завершённая или
// отменённая заявка reuse не получит.
func TestStartOptionsReusePolicy(t *testing.T) {
	policy := ExecutionPolicy{}.withDefaults()
	opts := startOptions("req-reuse", "task-queue", policy)

	if opts.WorkflowID != "fulfillment-req-reuse" {
		t.Fatalf("WorkflowID = %q, ожидался fulfillment-req-reuse", opts.WorkflowID)
	}
	if opts.TaskQueue != "task-queue" {
		t.Fatalf("TaskQueue = %q, ожидалась task-queue", opts.TaskQueue)
	}
	if opts.ExecutionTimeout != policy.ExecutionTimeout {
		t.Fatalf("ExecutionTimeout = %v, ожидался %v", opts.ExecutionTimeout, policy.ExecutionTimeout)
	}
	if opts.WorkflowIDReusePolicy != enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY {
		t.Fatalf("WorkflowIDReusePolicy = %v, ожидался ALLOW_DUPLICATE_FAILED_ONLY", opts.WorkflowIDReusePolicy)
	}
}

// Проверка типов: startOptions возвращает параметры запуска платформенного
// клиента, а не Temporal напрямую — адаптер не протекает SDK в порт.
var _ platformworkflow.StartOptions = startOptions("x", "q", ExecutionPolicy{})
