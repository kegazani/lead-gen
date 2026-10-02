package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

// EventEngagementObserved — зафиксировано квалифицированное взаимодействие.
const EventEngagementObserved EventName = "engagement-observed"

// EventHeader — обязательные поля события взаимодействия. Каждое доменное
// событие несёт их: event_id (ключ идемпотентности и дедупликации),
// occurred_at, tenant_id и сквозные correlation_id и causation_id.
//
// Пиксельных метрик и признака открытия в конверте нет по построению: контекст
// наблюдает только доставку, переход и ответ.
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

// DomainEvent — факт домена, произведённый агрегатом engagement-event.
type DomainEvent interface {
	// Name возвращает имя события.
	Name() EventName

	// OccurredAt возвращает время события.
	OccurredAt() time.Time

	// Header возвращает обязательные поля конверта события.
	Header() EventHeader
}

// EngagementObserved — квалифицированное взаимодействие зафиксировано.
//
// Событие несёт только результат квалификации: вид, уверенность и исход. Ни
// признака прочтения, ни пиксельных метрик здесь нет; для перехода дополнительно
// несётся непрозрачная ссылка и адрес назначения, чтобы потребитель мог связать
// факт с исходящим действием.
type EngagementObserved struct {
	EventHeader

	// EngagementEventID — идентификатор зафиксированного события взаимодействия.
	EngagementEventID EngagementEventID
	// CompanyID — компания, к которой отнесено взаимодействие.
	CompanyID CompanyID
	// Kind — квалифицированный вид взаимодействия.
	Kind EngagementKind
	// Confidence — уверенность в квалификации.
	Confidence SignalConfidence
	// Outcome — квалифицированный исход.
	Outcome Outcome
	// LinkRef — непрозрачная ссылка перехода (только для вида transition).
	LinkRef LinkRef
	// Target — адрес назначения перехода (только для вида transition).
	Target string
}

// Name реализует DomainEvent.
func (e EngagementObserved) Name() EventName { return EventEngagementObserved }

// OccurredAt реализует DomainEvent.
func (e EngagementObserved) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e EngagementObserved) Header() EventHeader { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
