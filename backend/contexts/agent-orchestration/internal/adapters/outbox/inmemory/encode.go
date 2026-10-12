// Package inmemory реализует transactional outbox и unit-of-work поверх
// оперативной памяти: изменение агрегата и запись его событий выполняются в одной
// транзакции, а публикацию подтверждённых записей выполняет relay, читающий журнал
// платформы (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни агрегата, ни события. Пакет не знает о брокере:
// журнал принимает готовые записи outbox, которые relay публикует в async/amqp.
package inmemory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Параметры публикации событий агента по транспорту async/amqp. Обменник —
// владелец контракта (контекст), ключ маршрутизации — "<event>.<schema>", а
// schema_version входит и в заголовки конверта.
const (
	exchangeName  = "agent-orchestration"
	schemaVersion = "v1"
)

// EncodeMessage превращает доменное событие агента в запись outbox.
//
// ID записи — event_id события: он же служит ключом идемпотентности публикации и
// дедупликации у потребителя, поэтому событие без event_id отвергается. Заголовки
// конверта несут обязательные поля события.
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
