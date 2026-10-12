package command

import (
	"errors"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// ErrTenantMismatch — агрегат принадлежит другому тенанту. Сценарий не раскрывает
// существование чужого агрегата: вызывающий получает отказ.
var ErrTenantMismatch = errors.New("сценарий: агрегат принадлежит другому тенанту")

// header собирает конверт события с новым event_id. tenant_id и occurred_at
// заполняет агрегат: событие не может «утечь» в чужой тенант.
func header(ids ports.IDGenerator, correlation, causation string) domain.EventHeader {
	var eventID domain.EventID
	if ids != nil {
		eventID = ids.NewEventID()
	}
	return domain.EventHeader{
		EventID:       eventID,
		CorrelationID: domain.CorrelationID(correlation),
		CausationID:   domain.CausationID(causation),
	}
}
