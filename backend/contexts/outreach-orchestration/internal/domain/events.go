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
type ActionApproved struct {
	CampaignID   CampaignID
	TenantID     TenantID
	DraftID      DraftID
	DraftVersion int
	DecisionID   DecisionID
	Actor        ActorID
	Channel      ActionChannel
	At           time.Time
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

// EventProducer — агрегат, публикующий свои неподтверждённые события.
// App-слой вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
