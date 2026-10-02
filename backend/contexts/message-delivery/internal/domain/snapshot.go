package domain

import (
	"fmt"
	"time"
)

// Сохранённое состояние агрегатов и его восстановление.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет снимки с экспортированными полями. Доменный объект из снимка
// собирается только через Rehydrate*, который проверяет инварианты и не эмитит
// события: восстановление не является новым фактом домена.

// MessageSnapshot — сохранённое состояние сообщения.
type MessageSnapshot struct {
	ID               MessageID
	TenantID         TenantID
	ApprovedActionID ApprovedActionID
	RecipientRef     RecipientRef
	Address          DeliveryAddress
	SenderDomain     SenderDomain
	IdempotencyKey   string
	Status           MessageStatus
	Attempts         []DeliveryAttempt
	CreatedAt        time.Time
}

// Snapshot возвращает состояние сообщения для сохранения.
func (m *Message) Snapshot() MessageSnapshot {
	attempts := make([]DeliveryAttempt, len(m.attempts))
	copy(attempts, m.attempts)
	return MessageSnapshot{
		ID:               m.id,
		TenantID:         m.tenantID,
		ApprovedActionID: m.approvedActionID,
		RecipientRef:     m.recipientRef,
		Address:          m.address,
		SenderDomain:     m.senderDomain,
		IdempotencyKey:   m.idempotencyKey,
		Status:           m.status,
		Attempts:         attempts,
		CreatedAt:        m.createdAt,
	}
}

// RehydrateMessage восстанавливает сообщение из сохранённого состояния.
//
// Проверяются обязательные части, известность состояния, непустой ключ
// идемпотентности и согласованность терминального состояния с журналом попыток:
// терминальное сообщение обязано нести хотя бы одну попытку, а незавершённое —
// не иметь терминального исхода в последней попытке.
func RehydrateMessage(s MessageSnapshot) (*Message, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := s.ApprovedActionID.validate(); err != nil {
		return nil, err
	}
	if err := s.RecipientRef.validate(); err != nil {
		return nil, err
	}
	if err := s.Address.validate(); err != nil {
		return nil, err
	}
	if err := s.SenderDomain.validate(); err != nil {
		return nil, err
	}
	if s.IdempotencyKey == "" {
		return nil, ErrIdempotencyKeyRequired
	}
	if !s.Status.isValid() {
		return nil, fmt.Errorf("%w: %q у сообщения %s", ErrInvalidMessageStatus, s.Status, s.ID)
	}
	if s.CreatedAt.IsZero() {
		return nil, ErrAttemptTimeRequired
	}

	attempts := make([]DeliveryAttempt, len(s.Attempts))
	for i, attempt := range s.Attempts {
		if attempt.Number != i+1 {
			return nil, fmt.Errorf(
				"%w: попытка %d сообщения %s имеет номер %d",
				ErrInvalidSnapshot, i+1, s.ID, attempt.Number,
			)
		}
		if err := attempt.validate(); err != nil {
			return nil, fmt.Errorf("%w: попытка %d сообщения %s: %v", ErrInvalidSnapshot, i+1, s.ID, err)
		}
		attempts[i] = attempt
	}
	if s.Status.IsTerminal() && len(attempts) == 0 {
		return nil, fmt.Errorf(
			"%w: терминальное сообщение %s (%q) без попыток",
			ErrInvalidSnapshot, s.ID, s.Status,
		)
	}

	return &Message{
		id:               s.ID,
		tenantID:         s.TenantID,
		approvedActionID: s.ApprovedActionID,
		recipientRef:     s.RecipientRef,
		address:          s.Address,
		senderDomain:     s.SenderDomain,
		idempotencyKey:   s.IdempotencyKey,
		status:           s.Status,
		attempts:         attempts,
		createdAt:        s.CreatedAt,
	}, nil
}
