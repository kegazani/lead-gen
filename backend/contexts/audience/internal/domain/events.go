package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемые контракты оборачивают событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventSegmentMembershipChanged — состав сегмента изменился: лид включён в
	// сегмент или исключён из него по итогам скоринга.
	EventSegmentMembershipChanged EventName = "segment-membership-changed"

	// EventAudienceSnapshotCreated — снимок аудитории зафиксирован и далее
	// неизменяем.
	EventAudienceSnapshotCreated EventName = "audience-snapshot-created"
)

// EventHeader — обязательные поля события контекста: event_id (ключ
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

// SegmentMembershipChanged — состав сегмента изменился после скоринга лида.
// Added отличает включение от исключения; Version — версия сегмента после
// изменения, MemberCount — размер сегмента после изменения.
type SegmentMembershipChanged struct {
	EventHeader
	SegmentID    SegmentID
	LeadID       LeadID
	Added        bool
	Value        ScoreValue
	ModelVersion ModelVersion
	MemberCount  int
	Version      int
}

// Name реализует DomainEvent.
func (e SegmentMembershipChanged) Name() EventName { return EventSegmentMembershipChanged }

// OccurredAt реализует DomainEvent.
func (e SegmentMembershipChanged) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e SegmentMembershipChanged) Header() EventHeader { return e.EventHeader }

// AudienceSnapshotCreated — снимок аудитории зафиксирован. Несёт версию
// сегмента, по которой построен снимок, и его размер.
type AudienceSnapshotCreated struct {
	EventHeader
	SnapshotID     SnapshotID
	SegmentID      SegmentID
	SegmentVersion int
	ModelVersion   ModelVersion
	MemberCount    int
	Version        int64
	GeneratedAt    time.Time
}

// Name реализует DomainEvent.
func (e AudienceSnapshotCreated) Name() EventName { return EventAudienceSnapshotCreated }

// OccurredAt реализует DomainEvent.
func (e AudienceSnapshotCreated) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e AudienceSnapshotCreated) Header() EventHeader { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
