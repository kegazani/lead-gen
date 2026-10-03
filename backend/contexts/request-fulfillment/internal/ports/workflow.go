package ports

import (
	"context"
	"errors"
)

// StartFulfillment — вход запуска исполнения заявки. Поля — только
// идентификаторы: оффер и его содержание в payload не поднимаются.
type StartFulfillment struct {
	RequestID     string
	TenantID      string
	CorrelationID string
	LeadTarget    int
}

// WorkflowRun — ссылка на запущенное исполнение workflow. AlreadyStarted
// отличает повторный старт уже существующего исполнения от только что
// созданного: повтор — не ошибка, а идемпотентный исход.
type WorkflowRun struct {
	WorkflowID     string
	RunID          string
	AlreadyStarted bool
}

// WorkflowStatus — наблюдаемое состояние исполнения заявки для API и консоли.
type WorkflowStatus struct {
	RequestID  string
	WorkflowID string
	RunID      string
	Phase      string
	Execution  string
	Found      int
	Drafted    int
	// NoLeads — отдельный наблюдаемый исход «0 уникальных лидов».
	NoLeads bool
}

// WorkflowStarter — порт запуска workflow исполнения заявки. Реализуется
// адаптером поверх platform/workflow; прикладной слой не зависит от Temporal.
type WorkflowStarter interface {
	StartFulfillment(ctx context.Context, in StartFulfillment) (WorkflowRun, error)
}

// WorkflowStatusReader — порт чтения состояния исполнения заявки.
type WorkflowStatusReader interface {
	WorkflowStatus(ctx context.Context, requestID string) (WorkflowStatus, error)
}

// WorkflowDecision — вход сигнала человеческой паузы workflow. Поля — только
// идентификаторы и решение: содержание оффера в payload не поднимается.
type WorkflowDecision struct {
	RequestID string
	Approved  bool
	Actor     string
}

// WorkflowSignaler — порт подачи сигнала утверждения результата (человеческая
// пауза workflow). Реализуется адаптером поверх platform/workflow; прикладной
// слой не зависит от Temporal.
type WorkflowSignaler interface {
	SignalApprovalDecision(ctx context.Context, in WorkflowDecision) error
}

// ErrWorkflowUnavailable — рантайм исполнения недоступен. Заявка при этом
// остаётся в текущем статусе: переход не выполняется, состояние не теряется.
var ErrWorkflowUnavailable = errors.New("исполнение: рантайм workflow недоступен")

// ErrWorkflowNotFound — исполнение заявки не найдено.
var ErrWorkflowNotFound = errors.New("исполнение: workflow заявки не найден")
