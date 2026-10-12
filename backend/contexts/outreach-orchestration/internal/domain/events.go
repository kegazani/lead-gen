package domain

import "time"

// EventName — имя domain event. Имена событий домена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемые контракты оборачивают их в
// envelope на границе адаптера.
type EventName string

const (
	// EventActionApproved — по точной версии черновика принято положительное
	// решение; черновик разрешён к исполнению.
	EventActionApproved EventName = "action-approved"

	// EventCampaignStateChanged — состояние кампании изменилось.
	EventCampaignStateChanged EventName = "campaign-state-changed"

	// EventOutreachOutcomeRecorded — результат шага последовательности: шаг
	// сформирован, приостановлен, остановлен, простой или реакция лида.
	EventOutreachOutcomeRecorded EventName = "outreach-outcome-recorded"
)

// DomainEvent — факт домена, произведённый агрегатом.
type DomainEvent interface {
	// Name возвращает имя события.
	Name() EventName

	// OccurredAt возвращает время события.
	OccurredAt() time.Time
}

// ActionApproved — событие о положительном решении по черновику.
//
// Несёт точную версию черновика: получателю разрешено исполнить именно эту
// версию, а более поздняя правка потребует нового решения (инвариант I2).
//
// Material — ссылка на версию материала из состава утверждённого черновика.
// Без неё публикуемое утверждённое действие не несло бы материал письма, и
// получатель не смог бы его разрешить: ссылка относится к точной версии
// черновика, поэтому принадлежит событию.
type ActionApproved struct {
	CampaignID   CampaignID
	TenantID     TenantID
	DraftID      DraftID
	DraftVersion int
	DecisionID   DecisionID
	Actor        ActorID
	Channel      ActionChannel
	Material     MaterialID
	// Recipient — непрозрачная ссылка на получателя черновика. Её несёт само
	// событие: идентификатор черновика (DraftID) — ссылка на материал, а не на
	// получателя, поэтому подстановка DraftID в recipientRef доставки неверна.
	Recipient RecipientID
	At        time.Time
}

// Name реализует DomainEvent.
func (e ActionApproved) Name() EventName { return EventActionApproved }

// OccurredAt реализует DomainEvent.
func (e ActionApproved) OccurredAt() time.Time { return e.At }

// CampaignStateChanged — событие о смене состояния кампании.
type CampaignStateChanged struct {
	CampaignID CampaignID
	TenantID   TenantID
	From       CampaignState
	To         CampaignState
	At         time.Time
}

// Name реализует DomainEvent.
func (e CampaignStateChanged) Name() EventName { return EventCampaignStateChanged }

// OccurredAt реализует DomainEvent.
func (e CampaignStateChanged) OccurredAt() time.Time { return e.At }

// OutreachOutcomeRecorded — событие о результате шага последовательности.
//
// Result описывает факт домена: шаг сформирован как черновик, шаг продвинул
// цепочку, участие приостановлено или остановлено, зафиксирован простой либо
// записана реакция лида. Событие не содержит персональных данных — только
// ссылки на кампанию, участие и лида.

type OutreachOutcomeRecorded struct {
	CampaignID  CampaignID
	TenantID    TenantID
	ExecutionID ExecutionID
	SequenceID  SequenceID
	Lead        LeadID
	Step        int
	Result      StepOutcome
	At          time.Time
}

// Name реализует DomainEvent.
func (e OutreachOutcomeRecorded) Name() EventName { return EventOutreachOutcomeRecorded }

// OccurredAt реализует DomainEvent.
func (e OutreachOutcomeRecorded) OccurredAt() time.Time { return e.At }

// EventProducer — агрегат, публикующий свои неподтверждённые события.
// App-слой вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
