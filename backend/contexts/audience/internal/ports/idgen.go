package ports

import "lead-gen/backend/contexts/audience/internal/domain"

// IDGenerator — источник идентификаторов событий и снимков аудитории. Инъекция
// генератора делает идентификаторы детерминированными в тестах и не привязывает
// домен к конкретному источнику случайности.
type IDGenerator interface {
	// NewEventID возвращает новый уникальный идентификатор события.
	NewEventID() domain.EventID

	// NewSnapshotID возвращает новый уникальный идентификатор снимка аудитории.
	NewSnapshotID() domain.SnapshotID
}
