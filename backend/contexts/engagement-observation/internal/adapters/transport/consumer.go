// Package transport реализует транспортный адаптер роли worker контекста
// engagement-observation: подписку на очередь события доставки и перенос его в
// прикладную форму команды приёма.
//
// Адаптер не содержит бизнес-правил: он разбирает тело сообщения, дедуплицирует
// повтор по event_id (доставка at-least-once, platform/messaging/inbox) и
// делегирует квалификацию сценарию command.IngestEvent. Пиксельных сигналов
// транспорт не переносит: событие доставки приходит от message-delivery как
// контракт delivery-event. Интерфейс подписки берётся у брокера
// (platform/transport/amqp.Consumer), поэтому отдельный порт не заводится.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/engagement-observation/internal/app/command"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// DefaultQueue — durable-очередь контекста по конвенции <downstream>.<contract>.
// Последний сегмент — имя контракта-события; по нему topic exchange разложит
// сообщение в очередь этого потребителя.
const DefaultQueue = "engagement-observation.delivery-event"

// Subscriber читает очередь брокера и квалифицирует события доставки через
// переданный обработчик.
type Subscriber struct {
	consumer amqptransport.Consumer
	deduper  inbox.Deduper
	queue    string
	ingest   *command.IngestEventHandler
}

// NewSubscriber собирает подписчика. consumer — порт чтения очереди брокера,
// deduper — журнал обработанных event_id, queue — имя очереди (пустое значение
// заменяется DefaultQueue).
func NewSubscriber(consumer amqptransport.Consumer, deduper inbox.Deduper, queue string, ingest *command.IngestEventHandler) *Subscriber {
	if strings.TrimSpace(queue) == "" {
		queue = DefaultQueue
	}
	return &Subscriber{consumer: consumer, deduper: deduper, queue: queue, ingest: ingest}
}

// Run читает очередь до отмены контекста. nil означает штатное завершение по
// отмене контекста.
//
// Порядок дедупликации: Record делается ПОСЛЕ эффекта обработчика — падение между
// эффектом и записью даёт повтор, а не потерю. Обработчик идемпотентен по
// event_id: входящий сигнал дедуплицируется ещё и журналом контекста.
func (s *Subscriber) Run(ctx context.Context) error {
	if s == nil || s.consumer == nil {
		return fmt.Errorf("transport: потребитель очереди не собран")
	}
	if s.deduper == nil {
		return fmt.Errorf("transport: нет журнала дедупликации очереди %q", s.queue)
	}
	if s.ingest == nil {
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
		if _, err := s.ingest.Handle(ctx, message.event); err != nil {
			return err
		}
		return s.deduper.Record(ctx, message.eventID)
	})
}

// deliveryEventDTO — тело сообщения очереди: событие доставки message-delivery.
// Событие доставки — вход policy pixel-free-observation: открытий в нём нет.
type deliveryEventDTO struct {
	EventID       string `json:"event_id"`
	TenantID      string `json:"tenant_id"`
	CompanyID     string `json:"company_id"`
	MessageID     string `json:"message_id"`
	Outcome       string `json:"outcome"`
	OccurredAt    string `json:"occurred_at"`
	CorrelationID string `json:"correlation_id"`
	CausationID   string `json:"causation_id"`
}

// message — разобранное сообщение: идентификатор для дедупликации и команда
// прикладной формы.
type message struct {
	eventID string
	event   command.IngestEvent
}

// decode разбирает тело доставки в прикладную форму. Идентификатор берётся из
// тела события, а при его отсутствии — из метаданных конверта (Delivery.EventID).
// Событие без event_id отвергается: дедуплицировать нечем.
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
	occurred, err := parseOccurred(dto.OccurredAt)
	if err != nil {
		return message{}, err
	}

	signalID := strings.TrimSpace(dto.MessageID)
	if signalID == "" {
		signalID = eventID
	}

	return message{
		eventID: eventID,
		event: command.IngestEvent{
			SignalID:      signalID,
			Source:        "message-delivery",
			TenantID:      dto.TenantID,
			CompanyID:     dto.CompanyID,
			Kind:          "delivery",
			Outcome:       dto.Outcome,
			OccurredAt:    occurred,
			CorrelationID: dto.CorrelationID,
			CausationID:   dto.CausationID,
		},
	}, nil
}

// parseOccurred разбирает время события: пустая строка — нулевое время (часы
// сборки подставят момент приёма), иначе — RFC3339.
func parseOccurred(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("transport: поле occurred_at должно быть в формате RFC3339")
	}
	return parsed, nil
}
