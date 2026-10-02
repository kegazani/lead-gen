package inmemory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Параметры публикации событий sales-workflow по транспорту async/amqp. Обменник
// назван по контексту-владельцу, ключ маршрутизации — "<contract>.<schema>", а
// schema_version входит и в заголовки конверта.
const (
	exchangeName  = "sales-workflow"
	schemaVersion = "v1"
)

// routingContract переводит имя доменного события в имя публикуемого контракта
// (architecture/domain-catalog.yaml, contracts.sales-workflow). У события
// crm-sync-conflict объявленного контракта нет: он остаётся локальным фактом и
// публикуется под собственным именем — без выдуманного потребителя.
func routingContract(name domain.EventName) string {
	switch name {
	case domain.EventDealStateChanged:
		return "deal-state"
	case domain.EventResultAttributed:
		return "attributed-result"
	case domain.EventCRMSyncConflict:
		return "crm-sync-conflict"
	default:
		return string(name)
	}
}

// EncodeMessage превращает доменное событие в запись outbox.
//
// ID записи — event_id события: он же служит ключом идемпотентности публикации и
// дедупликации у потребителя, поэтому событие без event_id отвергается. Заголовки
// конверта несут обязательные поля события и имя контракта.
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
	contract := routingContract(event.Name())
	return platformoutbox.Message{
		ID:         string(header.EventID),
		Exchange:   exchangeName,
		RoutingKey: fmt.Sprintf("%s.%s", contract, schemaVersion),
		Body:       body,
		Headers: map[string]string{
			"event_id":       string(header.EventID),
			"event_name":     string(event.Name()),
			"contract":       contract,
			"schema_version": schemaVersion,
			"occurred_at":    occurredAt.UTC().Format(time.RFC3339Nano),
			"tenant_id":      string(header.TenantID),
			"correlation_id": string(header.CorrelationID),
			"causation_id":   string(header.CausationID),
		},
	}, nil
}
