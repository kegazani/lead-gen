package domain

// PublicationStatus — value object «результат попытки публикации во внешней
// площадке». Промежуточные статусы (draft, approved, queued) отражают подготовку,
// терминальные (published, failed, rejected) — исход.
type PublicationStatus string

const (
	// StatusDraft — материал подготовлен и ждёт утверждения человеком.
	StatusDraft PublicationStatus = "draft"
	// StatusApproved — материал утверждён и допущен к публикации.
	StatusApproved PublicationStatus = "approved"
	// StatusQueued — публикация отложена ограничением частоты площадки.
	StatusQueued PublicationStatus = "queued"
	// StatusPublished — площадка приняла материал (терминальный).
	StatusPublished PublicationStatus = "published"
	// StatusFailed — площадка отклонила материал (терминальный).
	StatusFailed PublicationStatus = "failed"
	// StatusRejected — оператор отклонил материал (терминальный).
	StatusRejected PublicationStatus = "rejected"
)

var knownStatuses = map[PublicationStatus]struct{}{
	StatusDraft:     {},
	StatusApproved:  {},
	StatusQueued:    {},
	StatusPublished: {},
	StatusFailed:    {},
	StatusRejected:  {},
}

// Valid сообщает, объявлен ли статус доменом.
func (s PublicationStatus) Valid() bool {
	_, ok := knownStatuses[s]
	return ok
}

// IsTerminal сообщает, завершилось ли состояние публикации и меняться ли оно
// больше не может.
func (s PublicationStatus) IsTerminal() bool {
	switch s {
	case StatusPublished, StatusFailed, StatusRejected:
		return true
	default:
		return false
	}
}

// String возвращает имя статуса.
func (s PublicationStatus) String() string { return string(s) }

// canTransition сообщает, разрешён ли переход статуса статусной моделью.
//
// Модель выражает policy approved-publication-only: в published и failed ведут
// только approved и queued, то есть утверждённое состояние. Переход draft →
// published невозможен, поэтому публикация без утверждения недостижима.
func canTransition(from, to PublicationStatus) bool {
	switch from {
	case StatusDraft:
		return to == StatusApproved || to == StatusRejected
	case StatusApproved:
		return to == StatusQueued || to == StatusPublished || to == StatusFailed || to == StatusRejected
	case StatusQueued:
		return to == StatusApproved || to == StatusPublished || to == StatusFailed || to == StatusRejected
	default:
		return false
	}
}
