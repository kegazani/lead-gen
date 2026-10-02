package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml.
type EventName string

const (
	// EventCommunityDigestCreated — обезличенная выжимка построена. Совпадает с
	// доменным событием, объявленным каталогом для aggregate community-digest.
	EventCommunityDigestCreated EventName = "community-digest-created"
)

// EventHeader — обязательные поля события выжимки: event_id (ключ
// идемпотентности и дедупликации), occurred_at, tenant_id и сквозные
// correlation_id и causation_id.
type EventHeader struct {
	EventID       EventID
	OccurredAt    time.Time
	TenantID      TenantID
	CorrelationID CorrelationID
	CausationID   CausationID
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

// CommunityDigestCreated — выжимка построена. Несёт идентификаторы источника и
// периода, но не содержание и не авторов: событие обращается в зелёной зоне.
type CommunityDigestCreated struct {
	EventHeader
	DigestID      DigestID
	CommunityID   CommunityID
	PeriodFrom    time.Time
	PeriodTo      time.Time
	TopicCount    int
	CommunitySize int
}

// Name реализует DomainEvent.
func (e CommunityDigestCreated) Name() EventName { return EventCommunityDigestCreated }

// OccurredAt реализует DomainEvent: время берётся из конверта.
func (e CommunityDigestCreated) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e CommunityDigestCreated) Header() EventHeader { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
