package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventRequestSubmitted — заявка подана и получила статус submitted.
	EventRequestSubmitted EventName = "request-submitted"

	// EventRequestValidated — заявка прошла валидацию (validated).
	EventRequestValidated EventName = "request-validated"

	// EventRequestProcessingStarted — исполнение заявки запущено (processing).
	EventRequestProcessingStarted EventName = "request-processing-started"

	// EventRequestFulfilled — заявка исполнена (fulfilled).
	EventRequestFulfilled EventName = "request-fulfilled"

	// EventRequestRejected — заявка отклонена по итогам валидации (rejected).
	EventRequestRejected EventName = "request-rejected"

	// EventRequestCancelled — заявка отменена (cancelled).
	EventRequestCancelled EventName = "request-cancelled"

	// EventRequestReturnedForRevision — заявка возвращена клиенту на доработку
	// (needs-revision).
	EventRequestReturnedForRevision EventName = "request-returned-for-revision"

	// EventRequestResubmitted — клиент повторно подал доработанную заявку
	// (needs-revision → submitted).
	EventRequestResubmitted EventName = "request-resubmitted"

	// EventRequestExecutionFailed — исполнение заявки необратимо сломалось
	// (processing → failed). Несёт код и текст причины сбоя.
	EventRequestExecutionFailed EventName = "request-execution-failed"
	// EventExecutionIntentAccepted — владелец зафиксировал durable admission
	// исполнения вместе с lifecycle change в общей транзакции.
	EventExecutionIntentAccepted EventName = "execution-intent-accepted"
)

// EventHeader — обязательные поля события заявки. Каждое доменное событие несёт
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

// RequestSubmitted — заявка подана клиентом.
type RequestSubmitted struct {
	EventHeader
	RequestID     RequestID
	To            RequestStatus
	Actor         ActorID
	OfferRevision int
}

// Name реализует DomainEvent.
func (e RequestSubmitted) Name() EventName { return EventRequestSubmitted }

// RequestValidated — заявка прошла валидацию.
type RequestValidated struct {
	EventHeader
	RequestID     RequestID
	From          RequestStatus
	To            RequestStatus
	Actor         ActorID
	OfferRevision int
}

// Name реализует DomainEvent.
func (e RequestValidated) Name() EventName { return EventRequestValidated }

// RequestProcessingStarted — исполнение заявки запущено.
type RequestProcessingStarted struct {
	EventHeader
	RequestID     RequestID
	From          RequestStatus
	To            RequestStatus
	Actor         ActorID
	OfferRevision int
}

// Name реализует DomainEvent.
func (e RequestProcessingStarted) Name() EventName { return EventRequestProcessingStarted }

// RequestFulfilled — заявка исполнена. Несёт ссылки на артефакты фулфилмента,
// зафиксированные к моменту завершения.
type RequestFulfilled struct {
	EventHeader
	RequestID          RequestID
	From               RequestStatus
	To                 RequestStatus
	Actor              ActorID
	AudienceSnapshotID AudienceSnapshotID
	CampaignID         CampaignID
	DealID             DealID
}

// Name реализует DomainEvent.
func (e RequestFulfilled) Name() EventName { return EventRequestFulfilled }

// RequestRejected — заявка отклонена по итогам валидации. Причина обязательна.
type RequestRejected struct {
	EventHeader
	RequestID RequestID
	From      RequestStatus
	To        RequestStatus
	Actor     ActorID
	Reason    string
}

// Name реализует DomainEvent.
func (e RequestRejected) Name() EventName { return EventRequestRejected }

// RequestCancelled — заявка отменена из активного статуса.
type RequestCancelled struct {
	EventHeader
	RequestID RequestID
	From      RequestStatus
	To        RequestStatus
	Actor     ActorID
	Reason    string
}

// Name реализует DomainEvent.
func (e RequestCancelled) Name() EventName { return EventRequestCancelled }

// RequestReturnedForRevision — заявка возвращена клиенту на доработку из
// проверки или работы. Причина обязательна.
type RequestReturnedForRevision struct {
	EventHeader
	RequestID RequestID
	From      RequestStatus
	To        RequestStatus
	Actor     ActorID
	Reason    string
}

// Name реализует DomainEvent.
func (e RequestReturnedForRevision) Name() EventName { return EventRequestReturnedForRevision }

// RequestResubmitted — клиент повторно подал доработанную заявку на проверку.
type RequestResubmitted struct {
	EventHeader
	RequestID RequestID
	From      RequestStatus
	To        RequestStatus
	Actor     ActorID
	Reason    string
}

// Name реализует DomainEvent.
func (e RequestResubmitted) Name() EventName { return EventRequestResubmitted }

// RequestExecutionFailed — исполнение заявки необратимо сломалось. Несёт
// машинный код причины (ReasonCode) и текст сообщения оператору (Reason), актора
// и время перехода.
type RequestExecutionFailed struct {
	EventHeader
	RequestID  RequestID
	From       RequestStatus
	To         RequestStatus
	Actor      ActorID
	ReasonCode string
	Reason     string
}

// ExecutionIntentAccepted — durable команда исполнения принята владельцем.
// Событие не утверждает, что Temporal workflow уже стартовал.
type ExecutionIntentAccepted struct {
	EventHeader
	RequestID  RequestID
	CommandID  CommandID
	AttemptID  string
	Generation int64
	WorkflowID string
	Target     LeadTarget
	State      string
}

func (e ExecutionIntentAccepted) Name() EventName { return EventExecutionIntentAccepted }

// Name реализует DomainEvent.
func (e RequestExecutionFailed) Name() EventName { return EventRequestExecutionFailed }

// OccurredAt реализует DomainEvent для всех событий заявки: время берётся из
// конверта.
func (e RequestSubmitted) OccurredAt() time.Time           { return e.EventHeader.OccurredAt }
func (e RequestValidated) OccurredAt() time.Time           { return e.EventHeader.OccurredAt }
func (e RequestProcessingStarted) OccurredAt() time.Time   { return e.EventHeader.OccurredAt }
func (e RequestFulfilled) OccurredAt() time.Time           { return e.EventHeader.OccurredAt }
func (e RequestRejected) OccurredAt() time.Time            { return e.EventHeader.OccurredAt }
func (e RequestCancelled) OccurredAt() time.Time           { return e.EventHeader.OccurredAt }
func (e RequestReturnedForRevision) OccurredAt() time.Time { return e.EventHeader.OccurredAt }
func (e RequestResubmitted) OccurredAt() time.Time         { return e.EventHeader.OccurredAt }
func (e RequestExecutionFailed) OccurredAt() time.Time     { return e.EventHeader.OccurredAt }
func (e ExecutionIntentAccepted) OccurredAt() time.Time    { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent для всех событий заявки.
func (e RequestSubmitted) Header() EventHeader           { return e.EventHeader }
func (e RequestValidated) Header() EventHeader           { return e.EventHeader }
func (e RequestProcessingStarted) Header() EventHeader   { return e.EventHeader }
func (e RequestFulfilled) Header() EventHeader           { return e.EventHeader }
func (e RequestRejected) Header() EventHeader            { return e.EventHeader }
func (e RequestCancelled) Header() EventHeader           { return e.EventHeader }
func (e RequestReturnedForRevision) Header() EventHeader { return e.EventHeader }
func (e RequestResubmitted) Header() EventHeader         { return e.EventHeader }
func (e RequestExecutionFailed) Header() EventHeader     { return e.EventHeader }
func (e ExecutionIntentAccepted) Header() EventHeader    { return e.EventHeader }

// EventProducer — агрегат, публикующий свои неподтверждённые события. App-слой
// вычитывает их и записывает вместе с агрегатом (transactional outbox).
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}
