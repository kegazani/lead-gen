package domain

import "time"

// Provenance — происхождение доменного факта: идентификатор события, время,
// актор и сквозные идентификаторы. Агрегат не выдумывает эти значения: он
// получает их из прикладного слоя, а при записи события переносит в его конверт.
type Provenance struct {
	EventID       EventID
	At            time.Time
	Actor         ActorID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// validate проверяет обязательные части происхождения. CausationID допускается
// пустым: у корневой операции нет сообщения-причины.
func (p Provenance) validate() error {
	if err := p.EventID.validate(); err != nil {
		return err
	}
	if p.At.IsZero() {
		return ErrMissingTime
	}
	if err := p.Actor.validate(); err != nil {
		return err
	}
	return nil
}

// header собирает конверт события с заданным тенантом.
func (p Provenance) header(tenant TenantID) EventHeader {
	return EventHeader{
		EventID:       p.EventID,
		OccurredAt:    p.At,
		TenantID:      tenant,
		CorrelationID: p.CorrelationID,
		CausationID:   p.CausationID,
	}
}
