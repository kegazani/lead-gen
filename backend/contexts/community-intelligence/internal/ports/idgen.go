package ports

import "lead-gen/backend/contexts/community-intelligence/internal/domain"

// IDGenerator — источник идентификаторов событий и выжимок. Инъекция генератора
// делает идентификаторы детерминированными в тестах.
type IDGenerator interface {
	// NewEventID возвращает новый уникальный идентификатор события.
	NewEventID() domain.EventID

	// NewDigestID возвращает новый уникальный идентификатор выжимки.
	NewDigestID() domain.DigestID
}
