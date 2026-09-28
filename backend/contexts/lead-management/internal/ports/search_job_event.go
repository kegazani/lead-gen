package ports

import "time"

// SearchJobEventType — тип события жизненного цикла задания поиска.
type SearchJobEventType string

const (
	// EventEnqueued — задание поставлено в очередь.
	EventEnqueued SearchJobEventType = "enqueued"
	// EventTaken — задание взято обработчиком.
	EventTaken SearchJobEventType = "taken"
	// EventPhase — задание сменило фазу.
	EventPhase SearchJobEventType = "phase"
	// EventError — задание остановлено ошибкой с кодом.
	EventError SearchJobEventType = "error"
	// EventCancelRequested — оператор запросил отмену выполняющегося задания.
	EventCancelRequested SearchJobEventType = "cancel_requested"
	// EventCancelled — задание отменено.
	EventCancelled SearchJobEventType = "cancelled"
	// EventDone — задание завершилось успешно.
	EventDone SearchJobEventType = "done"
	// EventFailed — задание завершилось ошибкой.
	EventFailed SearchJobEventType = "failed"
)

// SearchJobEvent — событие жизненного цикла задания поиска. Операционная
// append-only запись (как search_job), а не доменное событие: фиксирует переходы
// задания для последующей агрегации админкой.
type SearchJobEvent struct {
	// JobID — идентификатор задания, к которому относится событие.
	JobID string
	// Type — тип события.
	Type SearchJobEventType
	// Code — машинный код отказа (для error/failed; пусто в остальных случаях).
	Code string
	// Progress — снимок счётчиков на момент события (для phase/terminal; nil в
	// остальных случаях).
	Progress *SearchJobProgress
	// CreatedAt — момент события.
	CreatedAt time.Time
}
