package ports

import "lead-gen/backend/contexts/engagement-observation/internal/domain"

// IDGenerator — источник идентификаторов агрегата и доменных событий. Инъекция
// генератора делает идентификаторы детерминированными в тестах и не привязывает
// домен к конкретному источнику случайности.
type IDGenerator interface {
	// NewEventID возвращает новый уникальный идентификатор. Им обозначается и
	// агрегат, и доменное событие: вызывающий решает, куда его направить.
	NewEventID() domain.EventID
}
