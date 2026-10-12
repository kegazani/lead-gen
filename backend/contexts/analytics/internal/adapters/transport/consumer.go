// Package transport реализует транспортный адаптер роли worker контекста
// analytics: подписку на очереди брокера и перенос нормализованных доменных
// событий в прикладную форму команды применения к проекции дашборда.
//
// Адаптер не содержит бизнес-правил: он разбирает тело сообщения, дедуплицирует
// повтор по event_id (доставка at-least-once, platform/messaging/inbox) и
// делегирует применение сценарию command.ApplyEvent. Проекцию наполняет
// прикладной слой, а не транспорт. Интерфейс подписки берётся у брокера
// (platform/transport/amqp.Consumer), поэтому отдельный порт не заводится.
//
// Витрину клиента питают два контракта: нормализованный analytics-event (ручные
// события владельца lead-management) и delivery-event (исходы доставки цикла
// владельца message-delivery). Декодер контракта передаётся подписчику, поэтому
// жизненный цикл очереди и дедупликация общие для обоих потоков.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// DefaultQueue — durable-очередь аналитики по конвенции <downstream>.<contract>.
// Последний сегмент — имя контракта-события; по нему topic exchange разложит
// сообщение в очередь этого потребителя.
const DefaultQueue = "analytics.analytics-event"

// DefaultDeliveryQueue — durable-очередь аналитики на исходы доставки цикла
// (контракт delivery-event владельца message-delivery). Отдельная очередь, потому
// что это другой контракт: topic exchange раскладывает его по привязке
// delivery-event.*. Она питает витрину клиента реальным ходом цикла
// (найдено → прокоммуницировано → …).
const DefaultDeliveryQueue = "analytics.delivery-event"

// decoder разбирает доставку очереди в прикладную форму сообщения. Разные
// контракты имеют разную форму тела, поэтому декодер передаётся подписчику.
type decoder func(amqptransport.Delivery) (message, error)

// Subscriber читает очередь брокера и применяет нормализованные доменные события
// к проекции через переданный обработчик.
type Subscriber struct {
	consumer amqptransport.Consumer
	deduper  inbox.Deduper
	queue    string
	decode   decoder
}

// NewSubscriber собирает подписчика на нормализованные события дашборда
// (контракт analytics-event). consumer — порт чтения очереди брокера, deduper —
// журнал обработанных event_id, queue — имя очереди (пустое значение заменяется
// DefaultQueue).
func NewSubscriber(consumer amqptransport.Consumer, deduper inbox.Deduper, queue string) *Subscriber {
	return newSubscriber(consumer, deduper, queueOrDefault(queue, DefaultQueue), decodeAnalyticsEvent)
}

// NewDeliveryEventSubscriber собирает подписчика на исходы доставки (контракт
// delivery-event владельца message-delivery): доставка нормализуется в проекцию
// дашборда клиента, поэтому рабочий контур питает витрину реальными событиями
// цикла. queue — имя очереди (пустое значение заменяется DefaultDeliveryQueue).
func NewDeliveryEventSubscriber(consumer amqptransport.Consumer, deduper inbox.Deduper, queue string) *Subscriber {
	return newSubscriber(consumer, deduper, queueOrDefault(queue, DefaultDeliveryQueue), decodeDeliveryEvent)
}

// newSubscriber — общая сборка подписчика: различие только в имени очереди по
// умолчанию и декодере контракта.
func newSubscriber(consumer amqptransport.Consumer, deduper inbox.Deduper, queue string, decode decoder) *Subscriber {
	return &Subscriber{consumer: consumer, deduper: deduper, queue: queue, decode: decode}
}

// queueOrDefault возвращает имя очереди или значение по умолчанию.
func queueOrDefault(queue, fallback string) string {
	if strings.TrimSpace(queue) == "" {
		return fallback
	}
	return queue
}

// Run читает очередь до отмены контекста, вызывая handle для каждого нового
// события. nil означает штатное завершение по отмене контекста.
//
// Порядок дедупликации: Record делается ПОСЛЕ эффекта обработчика — падение между
// эффектом и записью даёт повтор, а не потерю. Сообщение с уже обработанным
// event_id подтверждается без второго эффекта. Обработчик обязан быть
// идемпотентным по event_id: доставка at-least-once, повтор возможен даже после
// частично выполненного эффекта.
func (s *Subscriber) Run(ctx context.Context, handle func(context.Context, command.ApplyEvent) error) error {
	if s == nil || s.consumer == nil {
		return fmt.Errorf("transport: потребитель очереди не собран")
	}
	if s.deduper == nil {
		return fmt.Errorf("transport: нет журнала дедупликации очереди %q", s.queue)
	}
	if handle == nil {
		return fmt.Errorf("transport: нет обработчика очереди %q", s.queue)
	}

	decode := s.decode
	if decode == nil {
		decode = decodeAnalyticsEvent
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
		if err := handle(ctx, message.event); err != nil {
			return err
		}
		return s.deduper.Record(ctx, message.eventID)
	})
}

// eventDTO — тело сообщения очереди: нормализованное доменное событие дашборда
// плюс event_id для дедупликации. Форма полей совпадает с приёмом dev-контура
// (POST /dashboard/events), поэтому рабочий и dev-пути питают проекцию одинаково.
type eventDTO struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Kind       string `json:"kind"`
	CompanyID  string `json:"company_id"`
	Status     string `json:"status"`
	Outcome    string `json:"outcome"`
	OccurredAt string `json:"occurred_at"`
	ReplyText  string `json:"reply_text"`

	Source     string `json:"source"`
	Industry   string `json:"industry"`
	Segment    string `json:"segment"`
	CampaignID string `json:"campaign_id"`
	ManagerID  string `json:"manager_id"`

	// Поля исхода классификации (kind = lead-assessment): задание, вердикт и
	// категория причины. Необязательны для прочих видов событий.
	JobID   string `json:"job_id"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// message — разобранное сообщение: идентификатор для дедупликации и событие
// прикладной формы.
type message struct {
	eventID string
	event   command.ApplyEvent
}

// decodeAnalyticsEvent разбирает тело доставки нормализованного события дашборда
// в прикладную форму. Идентификатор берётся из тела события, а при его отсутствии
// — из метаданных конверта (Delivery.EventID): конверт переносится брокером
// отдельно от полезной нагрузки. Событие без event_id отвергается:
// дедуплицировать нечем.
func decodeAnalyticsEvent(delivery amqptransport.Delivery) (message, error) {
	var dto eventDTO
	if err := json.Unmarshal(delivery.Body, &dto); err != nil {
		return message{}, fmt.Errorf("transport: разбор события: %w", err)
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
	return message{
		eventID: eventID,
		event: command.ApplyEvent{
			TenantID:   dto.TenantID,
			Kind:       dto.Kind,
			CompanyID:  dto.CompanyID,
			Status:     dto.Status,
			Outcome:    dto.Outcome,
			OccurredAt: occurred,
			ReplyText:  dto.ReplyText,

			Source:     dto.Source,
			Industry:   dto.Industry,
			Segment:    dto.Segment,
			CampaignID: dto.CampaignID,
			ManagerID:  dto.ManagerID,

			JobID:   dto.JobID,
			Verdict: dto.Verdict,
			Reason:  dto.Reason,
		},
	}, nil
}

// deliveryEventDTO — тело контракта delivery-event (владелец message-delivery).
// Персональных данных не несёт: получатель — непрозрачная ссылка. Поля конверта
// (event_id, tenant_id, occurred_at) лежат в том же объекте конверта контракта.
type deliveryEventDTO struct {
	EventID      string `json:"event_id"`
	TenantID     string `json:"tenant_id"`
	OccurredAt   string `json:"occurred_at"`
	Outcome      string `json:"outcome"`
	CampaignID   string `json:"campaign_id"`
	RecipientRef string `json:"recipient_ref"`
	MessageID    string `json:"message_id"`
}

// decodeDeliveryEvent нормализует исход доставки цикла в форму проекции дашборда.
// Доставленное сообщение учитывается как коммуникация (вид message-sent): она и
// есть состоявшийся контакт с получателем. Отказ доставки — вид delivery-event с
// исходом failed (отбой). Идентификатор записи — непрозрачная ссылка получателя;
// при её отсутствии — идентификатор сообщения. Событие без тенанта или без
// принадлежности не создаёт запись: проекция не выдумывает срез.
func decodeDeliveryEvent(delivery amqptransport.Delivery) (message, error) {
	var dto deliveryEventDTO
	if err := json.Unmarshal(delivery.Body, &dto); err != nil {
		return message{}, fmt.Errorf("transport: разбор события доставки: %w", err)
	}
	eventID := strings.TrimSpace(dto.EventID)
	if eventID == "" {
		eventID = strings.TrimSpace(delivery.EventID)
	}
	if eventID == "" {
		return message{}, fmt.Errorf("transport: событие доставки без event_id")
	}
	occurred, err := parseOccurred(dto.OccurredAt)
	if err != nil {
		return message{}, err
	}
	company := strings.TrimSpace(dto.RecipientRef)
	if company == "" {
		company = strings.TrimSpace(dto.MessageID)
	}
	event := command.ApplyEvent{
		TenantID:   strings.TrimSpace(dto.TenantID),
		CompanyID:  company,
		Outcome:    strings.TrimSpace(dto.Outcome),
		OccurredAt: occurred,
		CampaignID: strings.TrimSpace(dto.CampaignID),
	}
	if isDeliveredOutcome(dto.Outcome) {
		// Доставка = состоявшаяся коммуникация: получатель учитывается как
		// прокоммуницированный.
		event.Kind = string(readmodel.KindMessageSent)
		event.Status = "sent"
	} else {
		// Отказ доставки — исход доставки: отбой, а не коммуникация.
		event.Kind = string(readmodel.KindDeliveryEvent)
	}
	return message{eventID: eventID, event: event}, nil
}

// isDeliveredOutcome сообщает, что исход доставки — успешная доставка.
func isDeliveredOutcome(outcome string) bool {
	return strings.EqualFold(strings.TrimSpace(outcome), "delivered")
}

// parseOccurred разбирает время события: пустая строка — нулевое время (проекция
// отнесёт компанию к дню без точки роста), иначе — RFC3339.
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
