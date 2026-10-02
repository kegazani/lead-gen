package domain

import (
	"fmt"
	"time"
)

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventInvoiceIssued — счёт за период выставлен в пределах активной подписки
	// и её лимитов.
	EventInvoiceIssued EventName = "invoice-issued"

	// EventBillingResultRecorded — результат биллинга зафиксирован в подписке.
	EventBillingResultRecorded EventName = "billing-result-recorded"

	// EventSuccessFeeCalculated — вознаграждение за подтверждённый результат
	// рассчитано.
	EventSuccessFeeCalculated EventName = "success-fee-calculated"
)

// BillingResultKind — вид результата биллинга в событии billing-result-recorded.
type BillingResultKind string

const (
	// BillingResultCharge — начисление по подписке.
	BillingResultCharge BillingResultKind = "charge"
	// BillingResultSuccessFee — вознаграждение за подтверждённый результат.
	BillingResultSuccessFee BillingResultKind = "success-fee"
)

// isValid сообщает, что вид результата объявлен доменом.
func (k BillingResultKind) isValid() bool {
	return k == BillingResultCharge || k == BillingResultSuccessFee
}

// String возвращает вид результата.
func (k BillingResultKind) String() string { return string(k) }

// EventHeader — обязательные поля события billing. Каждое доменное событие несёт
// их: event_id (ключ идемпотентности и дедупликации), occurred_at, tenant_id и
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
		return fmt.Errorf("%w: %v", ErrMissingEventID, err)
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

// InvoiceIssued — счёт за период выставлен.
type InvoiceIssued struct {
	EventHeader
	SubscriptionID SubscriptionID
	InvoiceID      InvoiceID
	Period         BillingPeriod
	Amount         Money
}

// Name реализует DomainEvent.
func (e InvoiceIssued) Name() EventName { return EventInvoiceIssued }

// BillingResultRecorded — результат биллинга зафиксирован в подписке.
type BillingResultRecorded struct {
	EventHeader
	SubscriptionID SubscriptionID
	ResultRef      ResultRef
	Kind           BillingResultKind
	Amount         Money
}

// Name реализует DomainEvent.
func (e BillingResultRecorded) Name() EventName { return EventBillingResultRecorded }

// SuccessFeeCalculated — вознаграждение за подтверждённый результат рассчитано.
// Событие несёт версию применённого правила расчёта.
type SuccessFeeCalculated struct {
	EventHeader
	SuccessFeeID   SuccessFeeID
	SubscriptionID SubscriptionID
	ResultRef      ResultRef
	DealID         DealID
	Base           Money
	Amount         Money
	RateBps        int64
	Calculation    CalculationVersion
}

// Name реализует DomainEvent.
func (e SuccessFeeCalculated) Name() EventName { return EventSuccessFeeCalculated }

// OccurredAt реализует DomainEvent для всех событий billing.
func (e InvoiceIssued) OccurredAt() time.Time         { return e.EventHeader.OccurredAt }
func (e BillingResultRecorded) OccurredAt() time.Time { return e.EventHeader.OccurredAt }
func (e SuccessFeeCalculated) OccurredAt() time.Time  { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent для всех событий billing.
func (e InvoiceIssued) Header() EventHeader         { return e.EventHeader }
func (e BillingResultRecorded) Header() EventHeader { return e.EventHeader }
func (e SuccessFeeCalculated) Header() EventHeader  { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
