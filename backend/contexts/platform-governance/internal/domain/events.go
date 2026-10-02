package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

// EventTenantAccessChanged — доступ тенанта изменён: зарегистрирован пользователь
// с ролью.
const EventTenantAccessChanged EventName = "tenant-access-changed"

// EventHeader — обязательные поля события тенант-доступа. Каждое доменное
// событие несёт их: event_id (ключ дедупликации), occurred_at и tenant_id.
type EventHeader struct {
	EventID    EventID
	OccurredAt time.Time
	TenantID   TenantID
}

// validate проверяет обязательные поля конверта события.
func (h EventHeader) validate() error {
	if err := h.EventID.validate(); err != nil {
		return err
	}
	if err := h.TenantID.validate(); err != nil {
		return err
	}
	if h.OccurredAt.IsZero() {
		return ErrMissingTime
	}
	return nil
}

// DomainEvent — факт домена, произведённый агрегатом.
type DomainEvent interface {
	// Name возвращает имя события.
	Name() EventName

	// OccurredAt возвращает время события.
	OccurredAt() time.Time

	// Header возвращает обязательные поля конверта события.
	Header() EventHeader
}

// TenantAccessChanged — пользователь тенанта зарегистрирован с ролью.
type TenantAccessChanged struct {
	EventHeader
	UserID UserID
	Login  string
	Role   Role
}

// Name реализует DomainEvent.
func (e TenantAccessChanged) Name() EventName { return EventTenantAccessChanged }

// OccurredAt реализует DomainEvent.
func (e TenantAccessChanged) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e TenantAccessChanged) Header() EventHeader { return e.EventHeader }
