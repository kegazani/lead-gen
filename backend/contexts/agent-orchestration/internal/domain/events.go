package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventPipelinePublished — опубликована новая версия пайплайна.
	EventPipelinePublished EventName = "agent-pipeline-published"

	// EventRunStarted — запуск агента начат.
	EventRunStarted EventName = "agent-run-started"

	// EventStepCompleted — шаг агента завершён успехом.
	EventStepCompleted EventName = "agent-step-completed"

	// EventStepFailed — шаг агента завершился отказом или недоступностью.
	EventStepFailed EventName = "agent-step-failed"

	// EventRunCompleted — запуск агента достиг терминального состояния.
	EventRunCompleted EventName = "agent-run-completed"

	// EventRunBlocked — запуск агента заблокирован: недоступность обязательного
	// шага или исчерпание попыток (требует вмешательства).
	EventRunBlocked EventName = "agent-run-blocked"
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

// PipelinePublished — опубликована версия пайплайна.
type PipelinePublished struct {
	EventHeader
	PipelineID PipelineID
	Version    int
	Steps      []PipelineStepID
	Blocks     []BlockRef
}

// Name реализует DomainEvent.
func (e PipelinePublished) Name() EventName { return EventPipelinePublished }

// OccurredAt реализует DomainEvent.
func (e PipelinePublished) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e PipelinePublished) Header() EventHeader { return e.EventHeader }

// RunStarted — запуск агента начат.
type RunStarted struct {
	EventHeader
	RunID      RunID
	MissionID  MissionID
	CompanyID  CompanyID
	PipelineID PipelineID
	Version    int
}

// Name реализует DomainEvent.
func (e RunStarted) Name() EventName { return EventRunStarted }

// OccurredAt реализует DomainEvent.
func (e RunStarted) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e RunStarted) Header() EventHeader { return e.EventHeader }

// StepCompleted — шаг агента завершён успехом. Несёт идентификаторы, блок, время
// (в конверте) и исход: достаточно для наблюдаемости шага и аналитики (Реш. 12).
type StepCompleted struct {
	EventHeader
	RunID        RunID
	StepRunID    StepRunID
	StepID       PipelineStepID
	BlockRef     BlockRef
	Order        int
	Outcome      BlockOutcome
	Attempts     int
	ArtifactRef  string
	DraftRef     DraftRef
	DraftVersion int
}

// Name реализует DomainEvent.
func (e StepCompleted) Name() EventName { return EventStepCompleted }

// OccurredAt реализует DomainEvent.
func (e StepCompleted) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e StepCompleted) Header() EventHeader { return e.EventHeader }

// StepFailed — шаг агента завершился отказом или недоступностью. Причина
// нормализована и не содержит секретов, токенов и персональных данных.
type StepFailed struct {
	EventHeader
	RunID     RunID
	StepRunID StepRunID
	StepID    PipelineStepID
	BlockRef  BlockRef
	Order     int
	Outcome   BlockOutcome
	Attempts  int
}

// Name реализует DomainEvent.
func (e StepFailed) Name() EventName { return EventStepFailed }

// OccurredAt реализует DomainEvent.
func (e StepFailed) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e StepFailed) Header() EventHeader { return e.EventHeader }

// RunCompleted — запуск агента достиг терминального состояния.
type RunCompleted struct {
	EventHeader
	RunID      RunID
	MissionID  MissionID
	CompanyID  CompanyID
	PipelineID PipelineID
	From       RunStatus
	To         RunStatus
	Reason     string
}

// Name реализует DomainEvent.
func (e RunCompleted) Name() EventName { return EventRunCompleted }

// OccurredAt реализует DomainEvent.
func (e RunCompleted) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e RunCompleted) Header() EventHeader { return e.EventHeader }

// RunBlocked — запуск агента заблокирован: недоступность обязательного шага или
// исчерпание попыток. Процесс не закрывается успехом (спека
// company-agent-orchestration).
type RunBlocked struct {
	EventHeader
	RunID       RunID
	MissionID   MissionID
	CompanyID   CompanyID
	From        RunStatus
	To          RunStatus
	CurrentStep PipelineStepID
	BlockRef    BlockRef
	Reason      string
}

// Name реализует DomainEvent.
func (e RunBlocked) Name() EventName { return EventRunBlocked }

// OccurredAt реализует DomainEvent.
func (e RunBlocked) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e RunBlocked) Header() EventHeader { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
