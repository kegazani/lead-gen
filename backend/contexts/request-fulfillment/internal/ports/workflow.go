package ports

import (
	"context"
	"errors"
)

// StartFulfillment — вход запуска исполнения заявки. Поля — только
// идентификаторы: оффер и его содержание в payload не поднимаются.
type StartFulfillment struct {
	RequestID        string
	TenantID         string
	CorrelationID    string
	LeadTarget       int
	ExecutionPurpose string
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
	// Target — явно заданная цель заявки по числу компаний (0 — цель не задана).
	// Нужна экрану, чтобы показать цель рядом с найденным и недобором.
	Target int
	// Shortfall — недобор: сколько компаний не хватает до цели заявки
	// (`max(0, цель − найдено)`). nil означает, что явная цель не задана: недобор
	// не рассчитывается и не показывается выдуманным нулём.
	Shortfall *int
	// NoLeads — отдельный наблюдаемый исход «0 уникальных лидов».
	NoLeads bool
	// PartialReason — стабильный безопасный код причины неполного поиска.
	PartialReason string
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

// WorkflowCanceller запрашивает отмену активного исполнения. Успех означает,
// что Temporal принял запрос; уже выполненный внешний эффект не отзывается.
type WorkflowCanceller interface {
	CancelWorkflow(ctx context.Context, requestID string) error
}

// WorkflowDecision — вход сигнала человеческой паузы workflow. Поля — только
// идентификаторы и решение: содержание оффера в payload не поднимается.
type WorkflowDecision struct {
	RequestID string
	Approved  bool
	Actor     string
}

// WorkflowSignaler — порт подачи сигнала утверждения результата (человеческая
// пауза workflow) и исхода доставки письма заявки. Реализуется адаптером поверх
// platform/workflow; прикладной слой не зависит от Temporal.
type WorkflowSignaler interface {
	SignalApprovalDecision(ctx context.Context, in WorkflowDecision) error
	// SignalDeliveryOutcome подаёт бегущему исполнению исход доставки письма
	// заявки (capability request-delivery-conclusion). Поля — только
	// идентификаторы, без PII.
	SignalDeliveryOutcome(ctx context.Context, in DeliveryOutcome) error
}

// DeliveryOutcome — вход сигнала исхода доставки письма заявки. Только
// идентификаторы: текста письма и PII здесь нет.
type DeliveryOutcome struct {
	// RequestID — заявка, которой принадлежит письмо.
	RequestID string
	// MessageID — идентификатор сообщения message-delivery (ключ дедупликации в
	// графе исполнения).
	MessageID string
	// Delivered — true, если письмо доставлено; false — исход недоставки.
	Delivered bool
}

// ErrWorkflowUnavailable — рантайм исполнения недоступен. Заявка при этом
// остаётся в текущем статусе: переход не выполняется, состояние не теряется.
var ErrWorkflowUnavailable = errors.New("исполнение: рантайм workflow недоступен")

// ErrWorkflowNotFound — исполнение заявки не найдено.
var ErrWorkflowNotFound = errors.New("исполнение: workflow заявки не найден")
