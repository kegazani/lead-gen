package outreachorchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	platformcontracts "lead-gen/backend/platform/contracts"
	outbox "lead-gen/backend/platform/messaging/outbox"
	amqp "lead-gen/backend/platform/transport/amqp"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
)

// ExchangeOutreachOrchestration — topic exchange контекста-владельца
// (design.md, Решение 3). Имя совпадает с идентификатором сервиса.
const ExchangeOutreachOrchestration = ServiceID

// AMQPSink связывает публикацию approved-action с транспортом.
//
// Он реализует relay.Sink среза: получив контракт, сериализует его, вычисляет
// ключ маршрутизации <contract>.<major> и ставит запись в transactional outbox
// платформы. Доставку в брокер выполняет outbox.Relay через amqp.Publisher:
// публикация подтверждается брокером (publisher confirm), а при сбое запись
// остаётся в журнале и повторяется. Повтор не создаёт второго эффекта, потому
// что получатель идемпотентен по event_id.
//
// Так «изменение агрегата сохранено, но событие не ушло» невозможно: доменный
// relay вызывает Sink только после commit, а транспортный outbox не теряет
// запись до подтверждения брокером.
type AMQPSink struct {
	journal *outbox.MemoryJournal
	relay   *outbox.Relay
}

// NewAMQPSink собирает адаптер публикации над издателем брокера. Некорректный
// интервал заменяется значением по умолчанию внутри outbox.NewRelay.
func NewAMQPSink(publisher amqp.Publisher, interval time.Duration) *AMQPSink {
	journal := outbox.NewMemoryJournal()
	return &AMQPSink{
		journal: journal,
		relay:   outbox.NewRelay(journal, publisher, interval),
	}
}

// Publish реализует relay.Sink. Контракт проверяется и записывается в outbox;
// ошибка означает, что запись не принята и доменный relay не подтвердит событие.
func (s *AMQPSink) Publish(ctx context.Context, action contracts.ApprovedAction) error {
	if err := action.Validate(); err != nil {
		return fmt.Errorf("sink approved-action: %w", err)
	}
	body, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("sink approved-action: сериализация контракта: %w", err)
	}
	routingKey, err := platformcontracts.RoutingKey(action.Name, action.SchemaVersion)
	if err != nil {
		return fmt.Errorf("sink approved-action: ключ маршрутизации: %w", err)
	}
	if err := s.journal.Append(ctx, outbox.Message{
		ID:         action.EventID,
		Exchange:   ExchangeOutreachOrchestration,
		RoutingKey: routingKey,
		Body:       body,
		Headers:    envelopeHeaders(action),
	}); err != nil {
		return fmt.Errorf("sink approved-action: постановка в outbox: %w", err)
	}
	return nil
}

// Run публикует записи транспортного outbox в брокер до отмены контекста.
// Ошибка отдельного прохода не теряет запись: она будет повторена.
func (s *AMQPSink) Run(ctx context.Context) error {
	return s.relay.Run(ctx)
}

// envelopeHeaders переносит метаданные конверта в заголовки сообщения. Имена
// совпадают с полями конверта (platform/contracts.Envelope) и ключом event_id
// брокера, поэтому потребитель дедуплицирует и коррелирует без разбора тела.
func envelopeHeaders(action contracts.ApprovedAction) map[string]string {
	headers := map[string]string{
		"event_id":       action.EventID,
		"name":           action.Name,
		"schema_version": action.SchemaVersion,
		"tenant_id":      action.TenantID,
		"correlation_id": action.CorrelationID,
	}
	if action.CausationID != "" {
		headers["causation_id"] = action.CausationID
	}
	return headers
}

// Утверждение, что адаптер удовлетворяет порт публикации среза. Проверка на
// этапе сборки, а не только по месту использования.
var _ relay.Sink = (*AMQPSink)(nil)
