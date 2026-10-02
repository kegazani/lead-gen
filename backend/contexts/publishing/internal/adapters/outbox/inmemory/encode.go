package inmemory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/publishing/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Параметры публикации событий публикации по транспорту async/amqp. Обменник —
// владелец контракта (контекст), ключ маршрутизации — "<event>.<schema>", а
// schema_version входит и в заголовки конверта.
const (
	exchangeName  = "publishing"
	schemaVersion = "v1"
)

// EncodeMessage превращает доменное событие публикации в запись outbox.
//
// ID записи — event_id события: он же служит ключом идемпотентности публикации и
// дедупликации у потребителя, поэтому событие без event_id отвергается.
func EncodeMessage(event domain.DomainEvent) (platformoutbox.Message, error) {
	if event == nil {
		return platformoutbox.Message{}, fmt.Errorf("outbox: пустое событие")
	}
	header := event.Header()
	if strings.TrimSpace(string(header.EventID)) == "" {
		return platformoutbox.Message{}, fmt.Errorf("outbox: событие %s без event_id", event.Name())
	}
	body, err := json.Marshal(event)
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
