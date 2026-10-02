package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventDealStateChanged — этап сделки изменился (создание или переход).
	EventDealStateChanged EventName = "deal-state-changed"

	// EventResultAttributed — результат сделки подтверждён и атрибутирован
	// исходящему действию.
	EventResultAttributed EventName = "result-attributed"

	// EventCRMSyncConflict — синхронизация с внешней CRM обнаружила конфликт
	// версий.
	EventCRMSyncConflict EventName = "crm-sync-conflict"
)

// EventHeader — обязательные поля события. Каждое доменное событие несёт их:
// event_id (ключ идемпотентности и дедупликации), occurred_at, tenant_id и
// сквозные correlation_id и causation_id.
type EventHeader struct {
	EventID       EventID       `json:"event_id"`
	OccurredAt    time.Time     `json:"occurred_at"`
	TenantID      TenantID      `json:"tenant_id"`
	CorrelationID CorrelationID `json:"correlation_id"`
	CausationID   CausationID   `json:"causation_id,omitempty"`
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

// DealStateChanged — этап сделки изменился. From пуст при создании сделки.
type DealStateChanged struct {
	EventHeader
	DealID DealID    `json:"deal_id"`
	From   DealStage `json:"from,omitempty"`
	To     DealStage `json:"to"`
	Actor  ActorID   `json:"actor"`
	Reason string    `json:"reason,omitempty"`
}

// Name реализует DomainEvent.
func (e DealStateChanged) Name() EventName { return EventDealStateChanged }

// ResultAttributed — результат сделки подтверждён и связан с исходящим действием.
type ResultAttributed struct {
	EventHeader
	DealID      DealID          `json:"deal_id"`
	ActionRef   ActionRef       `json:"action_ref"`
	EvidenceRef EvidenceRef     `json:"evidence_ref"`
	ExternalRef ExternalDealRef `json:"external_ref"`
	ConfirmedAt time.Time       `json:"confirmed_at"`
}

// Name реализует DomainEvent.
func (e ResultAttributed) Name() EventName { return EventResultAttributed }

// CRMSyncConflict — синхронизация обнаружила конфликт: внешняя версия не может
// быть применена без перезаписи локального состояния. Локальное состояние
// сохраняется, а конфликт фиксируется событием.
type CRMSyncConflict struct {
	EventHeader
	SyncID           CRMSyncID       `json:"crm_sync_id"`
	DealID           DealID          `json:"deal_id"`
	LocalRef         ExternalDealRef `json:"local_ref,omitempty"`
	LocalRevision    int64           `json:"local_revision"`
	ExternalRef      ExternalDealRef `json:"external_ref"`
	ExternalRevision int64           `json:"external_revision"`
	Reason           string          `json:"reason"`
}

// Name реализует DomainEvent.
func (e CRMSyncConflict) Name() EventName { return EventCRMSyncConflict }

// OccurredAt реализует DomainEvent для всех событий контекста: время берётся из
// конверта.
func (e DealStateChanged) OccurredAt() time.Time { return e.EventHeader.OccurredAt }
func (e ResultAttributed) OccurredAt() time.Time { return e.EventHeader.OccurredAt }
func (e CRMSyncConflict) OccurredAt() time.Time  { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent для всех событий контекста.
func (e DealStateChanged) Header() EventHeader { return e.EventHeader }
func (e ResultAttributed) Header() EventHeader { return e.EventHeader }
func (e CRMSyncConflict) Header() EventHeader  { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
