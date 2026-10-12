package ports

import (
	"context"
	"errors"
)

// ErrWorkflowUnavailable — рантайм долгих процессов недоступен. Запуск при этом
// остаётся в текущем состоянии: переход не выполняется, состояние не теряется.
var ErrWorkflowUnavailable = errors.New("порты: рантайм долгих процессов недоступен")

// ErrWorkflowNotFound — исполнение запуска агента не найдено в рантайме.
var ErrWorkflowNotFound = errors.New("порты: исполнение запуска не найдено")

// StartRunInput — вход запуска долгого процесса агента. Только идентификаторы и
// краткое описание задания: содержания чужих артефактов в payload нет
// (payload-гигиена, Реш. 2).
type StartRunInput struct {
	RunID           string
	TenantID        string
	MissionID       string
	CompanyID       string
	PipelineID      string
	PipelineVersion int
	Criteria        string
	CorrelationID   string
}

// RunHandle — ссылка на запущенное исполнение. AlreadyStarted отличает повторный
// старт уже существующего исполнения от только что созданного: повтор — не ошибка,
// а идемпотентный исход.
type RunHandle struct {
	WorkflowID     string
	RunID          string
	AlreadyStarted bool
}

// RunState — наблюдаемое состояние исполнения запуска для API и консоли.
type RunState struct {
	RunID       string
	WorkflowID  string
	Phase       string
	Execution   string
	Status      string
	CurrentStep string
	Cursor      int
}

// ApprovalDecisionInput — вход сигнала решения оператора по черновику. Только
// идентификаторы и решение: содержания черновика в payload нет.
type ApprovalDecisionInput struct {
	RunID        string
	TenantID     string
	DraftRef     string
	DraftVersion int
	Approved     bool
	Actor        string
	Reason       string
}

// StopInput — вход команды остановки запуска.
type StopInput struct {
	RunID    string
	TenantID string
	Actor    string
	Reason   string
}

// WorkflowRunner — порт долгих процессов: запуск исполнения агента, чтение его
// состояния, подача решения оператора и остановка. Реализуется адаптером поверх
// platform/workflow (Temporal); прикладной слой не зависит от рантайма
// (design.md, Реш. 2).
type WorkflowRunner interface {
	// StartRun запускает durable-исполнение запуска. Повтор с тем же RunID
	// идемпотентен.
	StartRun(ctx context.Context, in StartRunInput) (RunHandle, error)

	// RunState читает наблюдаемое состояние исполнения.
	RunState(ctx context.Context, runID string) (RunState, error)

	// SignalApproval подаёт решение оператора бегущему исполнению.
	SignalApproval(ctx context.Context, in ApprovalDecisionInput) error

	// StopRun подаёт команду остановки бегущему исполнению.
	StopRun(ctx context.Context, in StopInput) error
}
