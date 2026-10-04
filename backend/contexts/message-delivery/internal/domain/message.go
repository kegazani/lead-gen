package domain

import (
	"fmt"
	"strings"
	"time"
)

// MessageParams — параметры создания сообщения.
type MessageParams struct {
	ID               MessageID
	TenantID         TenantID
	ApprovedActionID ApprovedActionID
	RecipientRef     RecipientRef
	Address          DeliveryAddress
	SenderDomain     SenderDomain
	Subject          string
	Body             string
	IdempotencyKey   string
	CreatedAt        time.Time
}

// Message — aggregate root: одно исходящее почтовое сообщение, созданное по
// утверждённому действию.
//
// Поля закрыты, состояние меняется только методами перехода. Ключ
// идемпотентности неизменяем и служит естественным ключом повторной команды:
// повторная отправка с тем же ключом запрещена (инвариант message). Адрес
// назначения и отправляющий домен — value objects, поэтому в агрегат не попадает
// непроверенная строка. Тема и тело письма хранятся как часть состояния, чтобы
// повторная попытка могли быть выполнена без внешнего источника содержимого.
type Message struct {
	id               MessageID
	tenantID         TenantID
	approvedActionID ApprovedActionID
	recipientRef     RecipientRef
	address          DeliveryAddress
	senderDomain     SenderDomain
	subject          string
	body             string
	idempotencyKey   string
	status           MessageStatus
	attempts         []DeliveryAttempt
	createdAt        time.Time
	events           bufferEvents
}

// NewMessage создаёт сообщение в состоянии pending. Допустимость самой отправки
// (утверждённое действие, репутация, стоп-лист) проверяет политика и
// прикладной слой; конструктор проверяет форму агрегата.
func NewMessage(params MessageParams) (*Message, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.ApprovedActionID.validate(); err != nil {
		return nil, err
	}
	if err := params.RecipientRef.validate(); err != nil {
		return nil, err
	}
	if err := params.Address.validate(); err != nil {
		return nil, err
	}
	if err := params.SenderDomain.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return nil, ErrIdempotencyKeyRequired
	}
	if params.CreatedAt.IsZero() {
		return nil, ErrAttemptTimeRequired
	}
	return &Message{
		id:               params.ID,
		tenantID:         params.TenantID,
		approvedActionID: params.ApprovedActionID,
		recipientRef:     params.RecipientRef,
		address:          params.Address,
		senderDomain:     params.SenderDomain,
		subject:          params.Subject,
		body:             params.Body,
		idempotencyKey:   params.IdempotencyKey,
		status:           MessageStatusPending,
		createdAt:        params.CreatedAt,
	}, nil
}

// ID возвращает идентификатор сообщения.
func (m *Message) ID() MessageID { return m.id }

// TenantID возвращает тенант-владельца.
func (m *Message) TenantID() TenantID { return m.tenantID }

// ApprovedActionID возвращает утверждённое действие, санкционировавшее отправку.
func (m *Message) ApprovedActionID() ApprovedActionID { return m.approvedActionID }

// RecipientRef возвращает непрозрачную ссылку на получателя.
func (m *Message) RecipientRef() RecipientRef { return m.recipientRef }

// Address возвращает адрес назначения.
func (m *Message) Address() DeliveryAddress { return m.address }

// SenderDomain возвращает отправляющий домен.
func (m *Message) SenderDomain() SenderDomain { return m.senderDomain }

// Subject возвращает тему письма (может быть пустой, если содержимое не задано).
func (m *Message) Subject() string { return m.subject }

// Body возвращает тело письма (может быть пустым, если содержимое не задано).
func (m *Message) Body() string { return m.body }

// IdempotencyKey возвращает ключ идемпотентности сообщения.
func (m *Message) IdempotencyKey() string { return m.idempotencyKey }

// Status возвращает текущее состояние.
func (m *Message) Status() MessageStatus { return m.status }

// CreatedAt возвращает момент создания.
func (m *Message) CreatedAt() time.Time { return m.createdAt }

// Attempts возвращает копию журнала попыток в порядке выполнения.
func (m *Message) Attempts() []DeliveryAttempt {
	out := make([]DeliveryAttempt, len(m.attempts))
	copy(out, m.attempts)
	return out
}

// StartDelivery переводит сообщение из pending в sending. Повторный вызов для
// уже отправляемого или терминального сообщения запрещён.
func (m *Message) StartDelivery(at time.Time) error {
	if m.status != MessageStatusPending {
		return m.invalidTransition(MessageStatusSending)
	}
	if at.IsZero() {
		return ErrAttemptTimeRequired
	}
	m.status = MessageStatusSending
	return nil
}

// RecordDelivery фиксирует исход попытки и переводит сообщение в терминальное
// состояние. Номер попытки назначает агрегат. Доставленный исход порождает
// message-delivered, прочие — message-delivery-failed.
func (m *Message) RecordDelivery(outcome AttemptOutcome, providerCode string, at time.Time) error {
	if m.status.IsTerminal() {
		return m.invalidTransition(MessageStatusFailed)
	}
	if !outcome.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownAttemptOutcome, outcome)
	}
	if at.IsZero() {
		return ErrAttemptTimeRequired
	}

	attempt := DeliveryAttempt{
		Number:       len(m.attempts) + 1,
		Outcome:      outcome,
		ProviderCode: strings.TrimSpace(providerCode),
		At:           at,
	}
	if err := attempt.validate(); err != nil {
		return err
	}
	m.attempts = append(m.attempts, attempt)

	if outcome == OutcomeDelivered {
		m.status = MessageStatusDelivered
		m.events.append(MessageDelivered{
			MessageID:        m.id,
			TenantID:         m.tenantID,
			ApprovedActionID: m.approvedActionID,
			RecipientRef:     m.recipientRef,
			SenderDomain:     m.senderDomain,
			AttemptNumber:    attempt.Number,
			ProviderCode:     attempt.ProviderCode,
			At:               at,
		})
		return nil
	}

	m.status = MessageStatusFailed
	m.events.append(MessageDeliveryFailed{
		MessageID:        m.id,
		TenantID:         m.tenantID,
		ApprovedActionID: m.approvedActionID,
		RecipientRef:     m.recipientRef,
		SenderDomain:     m.senderDomain,
		AttemptNumber:    attempt.Number,
		Outcome:          outcome,
		ProviderCode:     attempt.ProviderCode,
		At:               at,
	})
	return nil
}

// IsRetryable сообщает, может ли сообщение быть переведено в повторную попытку:
// состояние failed, последняя попытка завершилась временным исходом,
// и исход не был терминальным решением провайдера.
func (m *Message) IsRetryable() bool {
	if m.status != MessageStatusFailed || len(m.attempts) == 0 {
		return false
	}
	return m.attempts[len(m.attempts)-1].Outcome == OutcomeTemporary
}

// LastOutcome возвращает исход последней попытки. Второе значение равно false,
// если попыток ещё нет.
func (m *Message) LastOutcome() (AttemptOutcome, bool) {
	if len(m.attempts) == 0 {
		return "", false
	}
	return m.attempts[len(m.attempts)-1].Outcome, true
}

// ReopenForRetry возвращает сообщение к отправке после временного исхода.
//
// Допустим только из состояния failed, когда последняя попытка завершилась
// временным исходом (сбой транспорта): такие сообщения — единственные, для
// которых повторная попытка допустима. Терминальные исходы провайдера
// (delivered, hard-bounce, soft-bounce) повторной попытке не подлежат.
func (m *Message) ReopenForRetry(at time.Time) error {
	if !m.IsRetryable() {
		return fmt.Errorf("%w: сообщение %s", ErrMessageNotRetryable, m.id)
	}
	if at.IsZero() {
		return ErrAttemptTimeRequired
	}
	m.status = MessageStatusSending
	return nil
}

// DomainEvents реализует EventProducer.
func (m *Message) DomainEvents() []DomainEvent { return m.events.all() }

// PullDomainEvents реализует EventProducer.
func (m *Message) PullDomainEvents() []DomainEvent { return m.events.pull() }

func (m *Message) invalidTransition(to MessageStatus) error {
	return fmt.Errorf(
		"%w: сообщение %s в состоянии %q не переходит в %q",
		ErrInvalidMessageTransition, m.id, m.status, to,
	)
}
