// Package transport реализует транспортный адаптер роли worker контекста
// request-fulfillment: подписку на очередь событий доставки (`delivery-event`,
// владелец message-delivery) и маршрутизацию исхода к бегущему исполнению заявки
// (capability request-delivery-conclusion).
//
// Адаптер не содержит бизнес-правил: он разбирает тело сообщения, дедуплицирует
// повтор по event_id (доставка at-least-once, platform/messaging/inbox) и
// делегирует сопоставление с заявкой сценарию command.ConcludeDelivery. Текста
// письма и персональных данных событие доставки не несёт. Интерфейс подписки
// берётся у брокера (platform/transport/amqp.Consumer), поэтому отдельный порт не
// заводится.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// DefaultQueue — durable-очередь контекста (конвенция <downstream>.<contract>).
// Последний сегмент — имя контракта-события; по нему topic exchange разложит
// сообщение в очередь этого потребителя.
const DefaultQueue = "request-fulfillment.delivery-event"

// Subscriber читает очередь брокера и маршрутизирует исходы доставки через
// переданный обработчик.
type Subscriber struct {
	consumer amqptransport.Consumer
	deduper  inbox.Deduper
	queue    string
	conclude *command.ConcludeDeliveryHandler
}

// NewSubscriber собирает подписчика. consumer — порт чтения очереди брокера,
// deduper — журнал обработанных event_id, queue — имя очереди (пустое значение
// заменяется DefaultQueue).
func NewSubscriber(consumer amqptransport.Consumer, deduper inbox.Deduper, queue string, conclude *command.ConcludeDeliveryHandler) *Subscriber {
	if strings.TrimSpace(queue) == "" {
		queue = DefaultQueue
	}
	return &Subscriber{consumer: consumer, deduper: deduper, queue: queue, conclude: conclude}
}

// Run читает очередь до отмены контекста. nil означает штатное завершение по
// отмене контекста.
//
// Порядок дедупликации: Record делается ПОСЛЕ эффекта обработчика — падение между
// эффектом и записью даёт повтор, а не потерю. Обработчик идемпотентен: сигнал в
// граф исполнения дедуплицируется по идентификатору сообщения, а повторный сигнал
// завершённому исполнению — no-op.
func (s *Subscriber) Run(ctx context.Context) error {
	if s == nil || s.consumer == nil {
		return fmt.Errorf("transport: потребитель очереди не собран")
	}
	if s.deduper == nil {
		return fmt.Errorf("transport: нет журнала дедупликации очереди %q", s.queue)
	}
	if s.conclude == nil {
		return fmt.Errorf("transport: нет обработчика очереди %q", s.queue)
	}

	return s.consumer.Consume(ctx, s.queue, func(ctx context.Context, delivery amqptransport.Delivery) error {
		message, err := decode(delivery)
		if err != nil {
			return err
		}
		seen, err := s.deduper.Seen(ctx, message.eventID)
		if err != nil {
			return err
		}
		if seen {
			// Повтор: эффект уже выполнен, сообщение подтверждается без второго
			// эффекта.
			return nil
		}
		if _, err := s.conclude.Handle(ctx, message.command); err != nil {
			return err
		}
		return s.deduper.Record(ctx, message.eventID)
	})
}

// deliveryEventDTO — тело сообщения очереди: событие доставки message-delivery.
// Разбираются только поля, нужные для маршрутизации: идентификаторы и исход.
// Текста письма и персональных данных в событии нет.
type deliveryEventDTO struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	MessageID  string `json:"message_id"`
	Outcome    string `json:"outcome"`
}

// message — разобранное сообщение: идентификатор для дедупликации и прикладная
// команда.
type message struct {
	eventID string
	command command.ConcludeDelivery
}

// decode разбирает тело доставки в прикладную команду. Идентификатор берётся из
// тела события, а при его отсутствии — из метаданных конверта (Delivery.EventID).
// Событие без event_id отвергается: дедуплицировать нечем. Неизвестный исход
// отвергается явно, а не проходит как «доставлено».
func decode(delivery amqptransport.Delivery) (message, error) {
	var dto deliveryEventDTO
	if err := json.Unmarshal(delivery.Body, &dto); err != nil {
		return message{}, fmt.Errorf("transport: разбор события доставки: %w", err)
	}
	eventID := strings.TrimSpace(dto.EventID)
	if eventID == "" {
		eventID = strings.TrimSpace(delivery.EventID)
	}
	if eventID == "" {
		return message{}, fmt.Errorf("transport: событие без event_id")
	}
	delivered, err := normalizeOutcome(dto.Outcome)
	if err != nil {
		return message{}, err
	}
	return message{
		eventID: eventID,
		command: command.ConcludeDelivery{
			TenantID:   strings.TrimSpace(dto.TenantID),
			CampaignID: strings.TrimSpace(dto.CampaignID),
			MessageID:  strings.TrimSpace(dto.MessageID),
			Delivered:  delivered,
		},
	}, nil
}

// normalizeOutcome переводит нормализованный исход доставки в признак доставки.
// Набор закрыт: `delivered` → true, `failed` → false, прочее отвергается.
func normalizeOutcome(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "delivered":
		return true, nil
	case "failed":
		return false, nil
	default:
		return false, fmt.Errorf("transport: неизвестный исход доставки %q", raw)
	}
}
