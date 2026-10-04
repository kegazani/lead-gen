package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

// Кодирование доменных событий в журнал outbox.
//
// Событие хранится парой (event_name, payload): имя задаёт форму, payload —
// значения. Отдельный DTO нужен потому, что value objects домена (SenderDomain)
// не имеют экспортируемых полей и не сериализуются стандартным json напрямую.

// eventPayload — адаптерная форма доменного события.
type eventPayload struct {
	MessageID        string    `json:"message_id"`
	TenantID         string    `json:"tenant_id"`
	ApprovedActionID string    `json:"approved_action_id"`
	RecipientRef     string    `json:"recipient_ref"`
	SenderDomain     string    `json:"sender_domain"`
	AttemptNumber    int       `json:"attempt_number"`
	Outcome          string    `json:"outcome,omitempty"`
	ProviderCode     string    `json:"provider_code,omitempty"`
	At               time.Time `json:"at"`
}

// encodeEvent кодирует доменное событие в пару (имя, payload).
func encodeEvent(event domain.DomainEvent) (string, []byte, error) {
	if event == nil {
		return "", nil, fmt.Errorf("postgres: пустое доменное событие")
	}

	var payload eventPayload
	switch e := event.(type) {
	case domain.MessageDelivered:
		payload = eventPayload{
			MessageID:        e.MessageID.String(),
			TenantID:         e.TenantID.String(),
			ApprovedActionID: e.ApprovedActionID.String(),
			RecipientRef:     e.RecipientRef.String(),
			SenderDomain:     e.SenderDomain.String(),
			AttemptNumber:    e.AttemptNumber,
			ProviderCode:     e.ProviderCode,
			At:               e.At,
		}
	case domain.MessageDeliveryFailed:
		payload = eventPayload{
			MessageID:        e.MessageID.String(),
			TenantID:         e.TenantID.String(),
			ApprovedActionID: e.ApprovedActionID.String(),
			RecipientRef:     e.RecipientRef.String(),
			SenderDomain:     e.SenderDomain.String(),
			AttemptNumber:    e.AttemptNumber,
			Outcome:          string(e.Outcome),
			ProviderCode:     e.ProviderCode,
			At:               e.At,
		}
	default:
		return "", nil, fmt.Errorf("postgres: неизвестное доменное событие %T", event)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("postgres: сериализация события %s: %w", event.Name(), err)
	}
	return string(event.Name()), raw, nil
}

// decodeEvent восстанавливает доменное событие по имени и payload.
func decodeEvent(name string, raw []byte) (domain.DomainEvent, error) {
	var payload eventPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("postgres: разбор события %s: %w", name, err)
	}
	sender, err := domain.ParseSenderDomain(payload.SenderDomain)
	if err != nil {
		return nil, fmt.Errorf("postgres: домен события %s: %w", name, err)
	}
	switch domain.EventName(name) {
	case domain.EventMessageDelivered:
		return domain.MessageDelivered{
			MessageID:        domain.MessageID(payload.MessageID),
			TenantID:         domain.TenantID(payload.TenantID),
			ApprovedActionID: domain.ApprovedActionID(payload.ApprovedActionID),
			RecipientRef:     domain.RecipientRef(payload.RecipientRef),
			SenderDomain:     sender,
			AttemptNumber:    payload.AttemptNumber,
			ProviderCode:     payload.ProviderCode,
			At:               payload.At,
		}, nil
	case domain.EventMessageDeliveryFailed:
		return domain.MessageDeliveryFailed{
			MessageID:        domain.MessageID(payload.MessageID),
			TenantID:         domain.TenantID(payload.TenantID),
			ApprovedActionID: domain.ApprovedActionID(payload.ApprovedActionID),
			RecipientRef:     domain.RecipientRef(payload.RecipientRef),
			SenderDomain:     sender,
			AttemptNumber:    payload.AttemptNumber,
			Outcome:          domain.AttemptOutcome(payload.Outcome),
			ProviderCode:     payload.ProviderCode,
			At:               payload.At,
		}, nil
	default:
		return nil, fmt.Errorf("postgres: неизвестное имя события %q", name)
	}
}
