package domain

import "fmt"

// RunStatus — value object run-status: состояние запуска агента.
//
// Статусная модель:
//
//	queued → running → awaiting-approval → running → completed
//	                    ↘ blocked → running | needs-attention
//	       ↘ stopped (терминальный)   running ↘ completed
//
// Терминальные состояния — completed, stopped, needs-attention; из них переходов
// нет (терминальное состояние не возобновляется). blocked — не терминально:
// недоступность обязательного шага держит обработку незавершённой, а не закрывает
// её.
type RunStatus string

const (
	// StatusQueued — запуск порождён и ждёт освобождения предела одновременной
	// обработки.
	StatusQueued RunStatus = "queued"

	// StatusRunning — запуск исполняется.
	StatusRunning RunStatus = "running"

	// StatusAwaitingApproval — запуск ожидает решения оператора по черновику
	// (человеческая пауза необратимого шага).
	StatusAwaitingApproval RunStatus = "awaiting-approval"

	// StatusBlocked — обязательный шаг недоступен: обработка не завершена и требует
	// устранения недоступности.
	StatusBlocked RunStatus = "blocked"

	// StatusCompleted — запуск завершён успехом (терминальный).
	StatusCompleted RunStatus = "completed"

	// StatusStopped — запуск остановлен командой оператора (терминальный).
	StatusStopped RunStatus = "stopped"

	// StatusNeedsAttention — попытки исчерпаны: запуск требует вмешательства
	// (терминальный).
	StatusNeedsAttention RunStatus = "needs-attention"
)

// RunStatuses возвращает согласованный набор статусов запуска.
func RunStatuses() []RunStatus {
	return []RunStatus{
		StatusQueued, StatusRunning, StatusAwaitingApproval, StatusBlocked,
		StatusCompleted, StatusStopped, StatusNeedsAttention,
	}
}

// isValid сообщает, что статус объявлен доменом.
func (s RunStatus) isValid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusAwaitingApproval, StatusBlocked,
		StatusCompleted, StatusStopped, StatusNeedsAttention:
		return true
	default:
		return false
	}
}

// validate отвергает статус вне согласованного набора.
func (s RunStatus) validate() error {
	if !s.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownRunStatus, s)
	}
	return nil
}

// IsTerminal сообщает, что статус терминален: из него нет переходов.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusStopped, StatusNeedsAttention:
		return true
	default:
		return false
	}
}

// IsActive сообщает, что запуск ещё не завершён (не терминален).
func (s RunStatus) IsActive() bool { return !s.IsTerminal() }

// OccupiesSlot сообщает, что запуск занимает слот одновременной обработки:
// исполняется, ждёт решения оператора или заблокирован. Запуск в очереди (queued)
// слот не занимает — супервизор считает занятые слоты против предела.
func (s RunStatus) OccupiesSlot() bool {
	switch s {
	case StatusRunning, StatusAwaitingApproval, StatusBlocked:
		return true
	default:
		return false
	}
}

// canTransitionTo проверяет допустимость перехода статусной модели.
//
//	queued            → running | stopped
//	running           → awaiting-approval | blocked | completed | needs-attention | stopped
//	awaiting-approval → running | blocked | needs-attention | stopped
//	blocked           → running | needs-attention | stopped
//
// Из терминальных статусов переходов нет.
func (s RunStatus) canTransitionTo(to RunStatus) bool {
	if s.IsTerminal() {
		return false
	}
	if !to.isValid() {
		return false
	}
	switch s {
	case StatusQueued:
		return to == StatusRunning || to == StatusStopped
	case StatusRunning:
		switch to {
		case StatusAwaitingApproval, StatusBlocked, StatusCompleted, StatusNeedsAttention, StatusStopped:
			return true
		}
		return false
	case StatusAwaitingApproval:
		switch to {
		case StatusRunning, StatusBlocked, StatusNeedsAttention, StatusStopped:
			return true
		}
		return false
	case StatusBlocked:
		switch to {
		case StatusRunning, StatusNeedsAttention, StatusStopped:
			return true
		}
		return false
	default:
		return false
	}
}
