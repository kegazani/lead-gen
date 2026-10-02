package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемые контракты оборачивают их в
// envelope на границе адаптера.
type EventName string

const (
	// EventMessageDelivered — сообщение принято провайдером.
	EventMessageDelivered EventName = "message-delivered"

	// EventMessageDeliveryFailed — доставка сообщения не состоялась.
	EventMessageDeliveryFailed EventName = "message-delivery-failed"
)

// DomainEvent — факт домена, произведённый агрегатом.
type DomainEvent interface {
	// Name возвращает имя события.
	Name() EventName

	// OccurredAt возвращает время события.
	OccurredAt() time.Time
}

// MessageDelivered — событие о принятии сообщения провайдером.
type MessageDelivered struct {
	MessageID        MessageID
	TenantID         TenantID
	ApprovedActionID ApprovedActionID
	RecipientRef     RecipientRef
	SenderDomain     SenderDomain
	AttemptNumber    int
	ProviderCode     string
	At               time.Time
}

// Name реализует DomainEvent.
func (e MessageDelivered) Name() EventName { return EventMessageDelivered }

// OccurredAt реализует DomainEvent.
func (e MessageDelivered) OccurredAt() time.Time { return e.At }

// MessageDeliveryFailed — событие об отказе доставки сообщения.
type MessageDeliveryFailed struct {
	MessageID        MessageID
	TenantID         TenantID
	ApprovedActionID ApprovedActionID
	RecipientRef     RecipientRef
	SenderDomain     SenderDomain
	AttemptNumber    int
	Outcome          AttemptOutcome
	ProviderCode     string
	At               time.Time
}

// Name реализует DomainEvent.
func (e MessageDeliveryFailed) Name() EventName { return EventMessageDeliveryFailed }

// OccurredAt реализует DomainEvent.
func (e MessageDeliveryFailed) OccurredAt() time.Time { return e.At }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}

// bufferEvents — вспомогательный буфер неподтверждённых событий агрегата.
type bufferEvents struct {
	events []DomainEvent
}

func (b *bufferEvents) append(events ...DomainEvent) {
	b.events = append(b.events, events...)
}

func (b *bufferEvents) all() []DomainEvent {
	out := make([]DomainEvent, len(b.events))
	copy(out, b.events)
	return out
}

func (b *bufferEvents) pull() []DomainEvent {
	out := b.all()
	b.events = nil
	return out
}
