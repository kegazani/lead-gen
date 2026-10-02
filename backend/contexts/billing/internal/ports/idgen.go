package ports

import "lead-gen/backend/contexts/billing/internal/domain"

// IDGenerator — источник идентификаторов событий домена. Инъекция генератора
// делает идентификаторы детерминированными в тестах и не привязывает домен к
// конкретному источнику случайности.
type IDGenerator interface {
	// NewEventID возвращает новый уникальный идентификатор события.
	NewEventID() domain.EventID
}
