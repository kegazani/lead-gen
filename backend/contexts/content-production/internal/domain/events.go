package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventContentVersionCreated — создана новая неизменяемая версия контента
	// (производит агрегат content-package).
	EventContentVersionCreated EventName = "content-version-created"

	// EventContentRendered — контент воспроизведён в артефакт (производит агрегат
	// render-job).
	EventContentRendered EventName = "content-rendered"
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

// ContentVersionCreated — создана новая неизменяемая версия контента.
type ContentVersionCreated struct {
	EventHeader
	AssetID   ContentAssetID
	VersionID ContentVersionID
	Revision  int
	Locale    string
}

// Name реализует DomainEvent.
func (e ContentVersionCreated) Name() EventName { return EventContentVersionCreated }

// OccurredAt реализует DomainEvent.
func (e ContentVersionCreated) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e ContentVersionCreated) Header() EventHeader { return e.EventHeader }

// ContentRendered — контент воспроизведён в артефакт указанного формата. Несёт
// точную версию контента и шаблон, по которым выполнен рендер.
type ContentRendered struct {
	EventHeader
	RenderJobID RenderJobID
	AssetID     ContentAssetID
	VersionID   ContentVersionID
	TemplateID  TemplateID
	Format      RenderFormat
	ArtifactRef ArtifactRef
}

// Name реализует DomainEvent.
func (e ContentRendered) Name() EventName { return EventContentRendered }

// OccurredAt реализует DomainEvent.
func (e ContentRendered) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e ContentRendered) Header() EventHeader { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}

// newEventHeader собирает конверт события из входных данных и проверяет его.
func newEventHeader(tenant TenantID, at time.Time, eventID EventID, correlation CorrelationID, causation CausationID) (EventHeader, error) {
	header := EventHeader{
		EventID:       eventID,
		OccurredAt:    at,
		TenantID:      tenant,
		CorrelationID: correlation,
		CausationID:   causation,
	}
	if err := header.validate(); err != nil {
		return EventHeader{}, err
	}
	return header, nil
}
