package ports

import "lead-gen/backend/contexts/agent-orchestration/internal/domain"

// IDGenerator — источник идентификаторов, не содержащих доменных правил.
// Идентификатор события служит ключом дедупликации у потребителя, поэтому
// генератор обязан давать уникальные значения.
type IDGenerator interface {
	// NewEventID возвращает новый идентификатор события.
	NewEventID() domain.EventID

	// NewRunID возвращает новый идентификатор запуска агента.
	NewRunID() domain.RunID
}
