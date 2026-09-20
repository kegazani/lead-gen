package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// contractDTO — контракт, который потребитель умеет проверять после разбора.
// Ограничение связывает разбор и валидацию: невалидное сообщение не доходит до
// обработчика.
type contractDTO interface {
	Validate() error
}

// subscribe — общая обвязка приёма асинхронного контракта. Она разбирает тело в
// антикоррупционный DTO потребителя, проверяет его, дедуплицирует по event_id и
// только затем вызывает обработчик.
//
// Порядок дедупликации важен: Record делается ПОСЛЕ эффекта обработчика. Падение
// между эффектом и записью даёт повтор, поэтому обработчик обязан быть
// идемпотентным по event_id (at-least-once, platform/messaging/inbox). Если
// event_id уже обработан, сообщение подтверждается без второго эффекта.
//
// Адаптер не содержит бизнес-правил: он только переносит контракт и делегирует
// обработку вызывающему.
func subscribe[T contractDTO](
	ctx context.Context,
	consumer amqptransport.Consumer,
	deduper inbox.Deduper,
	queue string,
	contract string,
	handle func(context.Context, T) error,
) error {
	if consumer == nil {
		return fmt.Errorf("transport: нет потребителя для очереди %q", queue)
	}
	if deduper == nil {
		return fmt.Errorf("transport: нет журнала дедупликации для контракта %s", contract)
	}
	if handle == nil {
		return fmt.Errorf("transport: нет обработчика контракта %s", contract)
	}

	return consumer.Consume(ctx, queue, func(ctx context.Context, delivery amqptransport.Delivery) error {
		var payload T
		if err := json.Unmarshal(delivery.Body, &payload); err != nil {
			return fmt.Errorf("transport: разбор %s: %w", contract, err)
		}
		if err := payload.Validate(); err != nil {
			return err
		}

		eventID := delivery.EventID
		if strings.TrimSpace(eventID) == "" {
			var meta struct {
				EventID string `json:"event_id"`
			}
			if err := json.Unmarshal(delivery.Body, &meta); err == nil {
				eventID = meta.EventID
			}
		}
		if strings.TrimSpace(eventID) == "" {
			return fmt.Errorf("transport: %s без event_id", contract)
		}

		seen, err := deduper.Seen(ctx, eventID)
		if err != nil {
			return err
		}
		if seen {
			// Повтор: эффект уже выполнен, сообщение подтверждается без второго
			// эффекта.
			return nil
		}
		if err := handle(ctx, payload); err != nil {
			return err
		}
		return deduper.Record(ctx, eventID)
	})
}

// EngagementEventSubscriber принимает контракт engagement-event по связи
// observation-to-outreach (интеграция event, транспорт async/amqp).
type EngagementEventSubscriber struct {
	consumer amqptransport.Consumer
	deduper  inbox.Deduper
	queue    string
}

// NewEngagementEventSubscriber собирает потребителя. Очередь — durable-очередь на
// потребителя и контракт (<downstream>.<contract>).
func NewEngagementEventSubscriber(consumer amqptransport.Consumer, deduper inbox.Deduper, queue string) *EngagementEventSubscriber {
	return &EngagementEventSubscriber{consumer: consumer, deduper: deduper, queue: queue}
}

// Run читает очередь и вызывает handle для каждого нового события.
// Обработчик обязан быть идемпотентным по event_id: доставка at-least-once.
// Run блокируется до отмены контекста и возвращает nil при штатном завершении.
func (s *EngagementEventSubscriber) Run(ctx context.Context, handle func(context.Context, contracts.EngagementEvent) error) error {
	if s == nil {
		return fmt.Errorf("transport: подписчик engagement-event не собран")
	}
	return subscribe(ctx, s.consumer, s.deduper, s.queue, contracts.ContractEngagementEvent, handle)
}

// ProcessingAuthoritySubscriber принимает контракт processing-authority по связи
// governance-policy-for-outreach (интеграция policy, транспорт async/amqp).
type ProcessingAuthoritySubscriber struct {
	consumer amqptransport.Consumer
	deduper  inbox.Deduper
	queue    string
}

// NewProcessingAuthoritySubscriber собирает потребителя политики.
func NewProcessingAuthoritySubscriber(consumer amqptransport.Consumer, deduper inbox.Deduper, queue string) *ProcessingAuthoritySubscriber {
	return &ProcessingAuthoritySubscriber{consumer: consumer, deduper: deduper, queue: queue}
}

// Run читает очередь и вызывает handle для каждого нового снимка политики.
// Повтор с тем же event_id не применяется второй раз.
func (s *ProcessingAuthoritySubscriber) Run(ctx context.Context, handle func(context.Context, contracts.ProcessingAuthority) error) error {
	if s == nil {
		return fmt.Errorf("transport: подписчик processing-authority не собран")
	}
	return subscribe(ctx, s.consumer, s.deduper, s.queue, contracts.ContractProcessingAuthority, handle)
}
