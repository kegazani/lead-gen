package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventMaterialPublished — утверждённый материал принят площадкой.
	EventMaterialPublished EventName = "material-published"
)

// EventHeader — обязательные поля события публикации. Каждое доменное событие
// несёт их: event_id (ключ идемпотентности и дедупликации), occurred_at,
// tenant_id и сквозные correlation_id и causation_id.
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

// MaterialPublished — утверждённый материал принят площадкой. Единственное
// доменное событие публикации: отказ площадки фиксируется состоянием агрегата и
// не объявляется фактом-событием, чтобы не выдавать неудачу за публикацию.
type MaterialPublished struct {
	EventHeader
	PublicationID PublicationID
	From          PublicationStatus
	To            PublicationStatus
	Actor         ActorID
	Platform      Platform
	Account       string
	Address       string
	ApprovedBy    ActorID
}

// Name реализует DomainEvent.
func (e MaterialPublished) Name() EventName { return EventMaterialPublished }

// OccurredAt реализует DomainEvent: время берётся из конверта.
func (e MaterialPublished) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e MaterialPublished) Header() EventHeader { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
