package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventProcessingAuthorityChanged — основание обработки создано, изменено или
	// отозвано.
	EventProcessingAuthorityChanged EventName = "processing-authority-changed"

	// EventContactSuppressed — контакт внесён в стоп-лист или снят с него.
	EventContactSuppressed EventName = "contact-suppressed"
)

// EventHeader — обязательные поля события. Каждое доменное событие несёт их:
// event_id (ключ идемпотентности и дедупликации), occurred_at, tenant_id и
// сквозные correlation_id и causation_id.
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

// AuthorityChangeKind — вид изменения основания обработки.
type AuthorityChangeKind string

const (
	// AuthorityCreated — основание создано.
	AuthorityCreated AuthorityChangeKind = "created"

	// AuthorityUpdated — условия основания изменены.
	AuthorityUpdated AuthorityChangeKind = "updated"

	// AuthorityRevoked — основание отозвано.
	AuthorityRevoked AuthorityChangeKind = "revoked"
)

// ProcessingAuthorityChanged — основание обработки изменилось: создано,
// обновлено или отозвано. Несёт снимок правил, чтобы потребитель (policy
// доставка) применил его локально.
type ProcessingAuthorityChanged struct {
	EventHeader

	BasisID       BasisID
	Change        AuthorityChangeKind
	Status        BasisStatus
	Purpose       string
	Category      DataCategory
	RetentionDays int
	ValidFrom     time.Time
	ValidUntil    time.Time
	Actor         ActorID
	Reason        string
}

// Name реализует DomainEvent.
func (e ProcessingAuthorityChanged) Name() EventName { return EventProcessingAuthorityChanged }

// SuppressionActionKind — вид изменения записи стоп-листа.
type SuppressionActionKind string

const (
	// SuppressionApplied — контакт внесён в стоп-лист.
	SuppressionApplied SuppressionActionKind = "suppressed"

	// SuppressionLifted — контакт снят со стоп-листа.
	SuppressionLifted SuppressionActionKind = "lifted"
)

// ContactSuppressed — контакт внесён в стоп-лист или снят с него.
type ContactSuppressed struct {
	EventHeader

	BasisID       BasisID
	SuppressionID SuppressionID
	Contact       ContactRef
	Action        SuppressionActionKind
	Actor         ActorID
	Reason        string
}

// Name реализует DomainEvent.
func (e ContactSuppressed) Name() EventName { return EventContactSuppressed }

// OccurredAt реализует DomainEvent для событий контекста: время берётся из
// конверта.
func (e ProcessingAuthorityChanged) OccurredAt() time.Time { return e.EventHeader.OccurredAt }
func (e ContactSuppressed) OccurredAt() time.Time          { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent для событий контекста.
func (e ProcessingAuthorityChanged) Header() EventHeader { return e.EventHeader }
func (e ContactSuppressed) Header() EventHeader          { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}

// eventHeader собирает заголовок доменного события.
func eventHeader(tenant TenantID, at time.Time, eventID EventID, correlation CorrelationID, causation CausationID) EventHeader {
	return EventHeader{
		EventID:       eventID,
		OccurredAt:    at,
		TenantID:      tenant,
		CorrelationID: correlation,
		CausationID:   causation,
	}
}
