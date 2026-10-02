package inmemory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/contracts/engagement-observation/engagementevent"
)

// Параметры публикации событий взаимодействия по транспорту async/amqp.
// Обменник — владелец контракта (контекст), ключ маршрутизации — "<event>.<schema>",
// а schema_version входит и в заголовки конверта.
const (
	exchangeName  = "engagement-observation"
	schemaVersion = "v1"
)

// EncodeMessage превращает доменное событие взаимодействия в запись outbox.
//
// На границе адаптера доменное событие переводится в опубликованный контракт
// engagement-event и проверяется его Validate: в брокер не попадает форма,
// которую потребитель не сможет разобрать. ID записи — event_id события: он же
// служит ключом идемпотентности публикации и дедупликации у потребителя.
func EncodeMessage(event domain.DomainEvent) (platformoutbox.Message, error) {
	if event == nil {
		return platformoutbox.Message{}, fmt.Errorf("outbox: пустое событие")
	}
	header := event.Header()
	if strings.TrimSpace(string(header.EventID)) == "" {
		return platformoutbox.Message{}, fmt.Errorf("outbox: событие %s без event_id", event.Name())
	}

	contract, err := toContract(event)
	if err != nil {
		return platformoutbox.Message{}, err
	}
	if err := contract.Validate(); err != nil {
		return platformoutbox.Message{}, err
	}

	body, err := json.Marshal(contract)
	if err != nil {
		return platformoutbox.Message{}, fmt.Errorf("outbox: сериализация события %s: %w", event.Name(), err)
	}
	occurredAt := header.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return platformoutbox.Message{
		ID:         string(header.EventID),
		Exchange:   exchangeName,
		RoutingKey: fmt.Sprintf("%s.%s", event.Name(), schemaVersion),
		Body:       body,
		Headers: map[string]string{
			"event_id":       string(header.EventID),
			"event_name":     string(event.Name()),
			"schema_version": schemaVersion,
			"occurred_at":    occurredAt.UTC().Format(time.RFC3339Nano),
			"tenant_id":      string(header.TenantID),
			"correlation_id": string(header.CorrelationID),
			"causation_id":   string(header.CausationID),
		},
	}, nil
}

// toContract переводит доменное событие взаимодействия в форму опубликованного
// контракта. Пиксельных метрик в контракте нет.
func toContract(event domain.DomainEvent) (engagementevent.EngagementEvent, error) {
	observed, ok := event.(domain.EngagementObserved)
	if !ok {
		return engagementevent.EngagementEvent{}, fmt.Errorf("outbox: событие %s не имеет контрактной формы", event.Name())
	}
	header := observed.Header()
	return engagementevent.EngagementEvent{
		Envelope: engagementevent.Envelope{
			EventID:       string(header.EventID),
			OccurredAt:    header.OccurredAt,
			TenantID:      string(header.TenantID),
			CorrelationID: string(header.CorrelationID),
			CausationID:   string(header.CausationID),
			Name:          engagementevent.ContractName,
			SchemaVersion: engagementevent.SchemaVersion,
		},
		EngagementEventID: string(observed.EngagementEventID),
		CompanyID:         string(observed.CompanyID),
		Kind:              string(observed.Kind),
		Confidence:        string(observed.Confidence),
		Outcome:           string(observed.Outcome),
		LinkRef:           string(observed.LinkRef),
		Target:            observed.Target,
	}, nil
}
