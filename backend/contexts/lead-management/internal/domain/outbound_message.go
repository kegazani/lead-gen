package domain

import (
	"fmt"
	"strings"
	"time"
)

// OutboundMessageID — идентификатор исходящего сообщения. В срезе генерируется
// прикладным слоем (idempotency key), домен лишь требует непустое значение.
type OutboundMessageID string

// validate проверяет непустоту идентификатора.
func (id OutboundMessageID) validate() error {
	if strings.TrimSpace(string(id)) == "" {
		return ErrEmptyMessageID
	}
	return nil
}

// String возвращает идентификатор строкой.
func (id OutboundMessageID) String() string { return string(id) }

// MessageStatus — состояние исходящего сообщения (value object message-status).
type MessageStatus string

// Согласованные состояния исходящего сообщения. Помимо жизненного цикла
// (queued/sent/failed) сюда входят терминальные исходы доставки: bounced,
// unsubscribed и rejected — именно они подавляют дальнейшую рассылку по лиду
// (capability lead-email).
const (
	MessageStatusQueued       MessageStatus = "queued"
	MessageStatusSent         MessageStatus = "sent"
	MessageStatusFailed       MessageStatus = "failed"
	MessageStatusBounced      MessageStatus = "bounced"
	MessageStatusUnsubscribed MessageStatus = "unsubscribed"
	MessageStatusRejected     MessageStatus = "rejected"
)

// valid сообщает, известно ли состояние.
func (s MessageStatus) valid() bool {
	switch s {
	case MessageStatusQueued, MessageStatusSent, MessageStatusFailed,
		MessageStatusBounced, MessageStatusUnsubscribed, MessageStatusRejected:
		return true
	default:
		return false
	}
}

// Suppresses сообщает, подавляет ли исход дальнейшую рассылку по лиду: bounced,
// unsubscribed и rejected исключают лид из сегмента (инвариант каталога).
func (s MessageStatus) Suppresses() bool {
	switch s {
	case MessageStatusBounced, MessageStatusUnsubscribed, MessageStatusRejected:
		return true
	default:
		return false
	}
}

// String возвращает состояние строкой.
func (s MessageStatus) String() string { return string(s) }

// OutboundMessage — aggregate root outbound-message (владелец lead-management):
// журнал отправки письма. Получатель обязателен; отправка на подавленный адрес
// запрещена; лог неизменяем после фиксации исхода. Ссылка на шаблон и собранное
// тело фиксируются на момент создания, чтобы история не менялась при правке
// шаблона.
type OutboundMessage struct {
	id        OutboundMessageID
	leadID    CompanyID
	recipient string
	template  string
	body      string
	status    MessageStatus
	reason    string
	createdAt time.Time
	updatedAt time.Time
	events    []DomainEvent
}

// CreateOutboundMessageParams — параметры создания исходящего сообщения.
type CreateOutboundMessageParams struct {
	// ID — идентификатор сообщения (idempotency key).
	ID string
	// LeadID — идентификатор лида (компании), которому отправлено письмо. Ссылка
	// на агрегат lead-profile хранится идентификатором.
	LeadID string
	// Recipient — адрес получателя (обязателен).
	Recipient string
	// Template — ссылка на шаблон письма.
	Template string
	// Body — собранное тело письма.
	Body string
	// Suppressed — адрес подавлен (bounced/unsubscribed/rejected); отправка на
	// него запрещена инвариантом.
	Suppressed bool
	// CreatedAt — момент создания.
	CreatedAt time.Time
}

// CreateOutboundMessage создаёт исходящее сообщение в состоянии queued.
// Получатель обязателен; отправка на подавленный адрес запрещена.
func CreateOutboundMessage(params CreateOutboundMessageParams) (*OutboundMessage, error) {
	id := OutboundMessageID(strings.TrimSpace(params.ID))
	if err := id.validate(); err != nil {
		return nil, err
	}
	leadID := CompanyID(strings.TrimSpace(params.LeadID))
	if err := leadID.validate(); err != nil {
		return nil, fmt.Errorf("%w: сообщение %s", err, id)
	}
	if strings.TrimSpace(params.Recipient) == "" {
		return nil, fmt.Errorf("%w: сообщение %s", ErrEmptyRecipient, id)
	}
	if params.Suppressed {
		return nil, fmt.Errorf("%w: %s", ErrSuppressedRecipient, strings.TrimSpace(params.Recipient))
	}
	now := params.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return &OutboundMessage{
		id:        id,
		leadID:    leadID,
		recipient: strings.TrimSpace(params.Recipient),
		template:  params.Template,
		body:      params.Body,
		status:    MessageStatusQueued,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// ID возвращает идентификатор сообщения.
func (m *OutboundMessage) ID() OutboundMessageID { return m.id }

// LeadID возвращает идентификатор лида, которому отправлено письмо.
func (m *OutboundMessage) LeadID() CompanyID { return m.leadID }

// Recipient возвращает адрес получателя.
func (m *OutboundMessage) Recipient() string { return m.recipient }

// Template возвращает ссылку на шаблон письма.
func (m *OutboundMessage) Template() string { return m.template }

// Body возвращает собранное тело письма.
func (m *OutboundMessage) Body() string { return m.body }

// Status возвращает состояние сообщения.
func (m *OutboundMessage) Status() MessageStatus { return m.status }

// Reason возвращает причину отказа (пусто, если исход не failed/bounced).
func (m *OutboundMessage) Reason() string { return m.reason }

// CreatedAt возвращает момент создания.
func (m *OutboundMessage) CreatedAt() time.Time { return m.createdAt }

// UpdatedAt возвращает момент последнего изменения.
func (m *OutboundMessage) UpdatedAt() time.Time { return m.updatedAt }

// MarkSent фиксирует успешную отправку. Эмитит domain event message-sent.
func (m *OutboundMessage) MarkSent(at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	m.status = MessageStatusSent
	m.reason = ""
	m.updatedAt = at
	m.events = append(m.events, MessageSent{
		MessageID: m.id.String(),
		Recipient: m.recipient,
		At:        at.Format(time.RFC3339),
	})
}

// MarkFailed фиксирует отказ отправки с причиной.
func (m *OutboundMessage) MarkFailed(reason string, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	m.status = MessageStatusFailed
	m.reason = reason
	m.updatedAt = at
}

// MarkBounced фиксирует отказ доставки (bounce) с причиной.
func (m *OutboundMessage) MarkBounced(reason string, at time.Time) {
	m.markSuppression(MessageStatusBounced, reason, at)
}

// MarkUnsubscribed фиксирует отписку получателя с причиной.
func (m *OutboundMessage) MarkUnsubscribed(reason string, at time.Time) {
	m.markSuppression(MessageStatusUnsubscribed, reason, at)
}

// MarkRejected фиксирует постоянный отказ адреса с причиной.
func (m *OutboundMessage) MarkRejected(reason string, at time.Time) {
	m.markSuppression(MessageStatusRejected, reason, at)
}

// markSuppression фиксирует подавляющий исход доставки с причиной.
func (m *OutboundMessage) markSuppression(status MessageStatus, reason string, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	m.status = status
	m.reason = reason
	m.updatedAt = at
}

// Events возвращает и очищает буфер событий агрегата.
func (m *OutboundMessage) Events() []DomainEvent {
	events := m.events
	m.events = nil
	return events
}

// OutboundMessageSnapshot — сохранённое состояние aggregate root outbound-message.
type OutboundMessageSnapshot struct {
	ID        OutboundMessageID
	LeadID    CompanyID
	Recipient string
	Template  string
	Body      string
	Status    MessageStatus
	Reason    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Snapshot возвращает состояние агрегата для сохранения.
func (m *OutboundMessage) Snapshot() OutboundMessageSnapshot {
	return OutboundMessageSnapshot{
		ID:        m.id,
		LeadID:    m.leadID,
		Recipient: m.recipient,
		Template:  m.template,
		Body:      m.body,
		Status:    m.status,
		Reason:    m.reason,
		CreatedAt: m.createdAt,
		UpdatedAt: m.updatedAt,
	}
}

// RehydrateOutboundMessage восстанавливает сообщение из сохранённого состояния,
// проверяя идентификатор, получателя и согласованность состояния. События не
// эмитятся.
func RehydrateOutboundMessage(s OutboundMessageSnapshot) (*OutboundMessage, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.LeadID.validate(); err != nil {
		return nil, fmt.Errorf("%w: сообщение %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if strings.TrimSpace(s.Recipient) == "" {
		return nil, fmt.Errorf("%w: сообщение %s", ErrEmptyRecipient, s.ID)
	}
	if !s.Status.valid() {
		return nil, fmt.Errorf("%w: сообщение %s: неизвестное состояние %q", ErrInvalidSnapshot, s.ID, s.Status)
	}
	return &OutboundMessage{
		id:        s.ID,
		leadID:    s.LeadID,
		recipient: strings.TrimSpace(s.Recipient),
		template:  s.Template,
		body:      s.Body,
		status:    s.Status,
		reason:    s.Reason,
		createdAt: s.CreatedAt,
		updatedAt: s.UpdatedAt,
	}, nil
}
