// Package relay публикует события transactional outbox после commit.
//
// Изменение агрегата и запись события message-delivered / message-delivery-failed
// выполняются в одной транзакции; публикация следует за успешным commit и может
// повториться, если доставка или подтверждение прервались. Relay не знает о
// брокере и базе: он читает ожидающие сообщения из Source и отдаёт готовый
// контракт delivery-event в Sink. Повторная доставка не создаёт второго внешнего
// эффекта, потому что получатель идемпотентен по event_id и ключу
// (tenant_id, message_id, attempt_number).
//
// Интерфейсы Source и Sink объявлены здесь намеренно: назначение публикации
// задаётся в рамках среза, без подключения реальной очереди или внешней системы.
package relay

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/contracts/message-delivery/deliveryevent"
)

// Message — запись outbox, ожидающая публикации.
type Message struct {
	// ID — стабильный идентификатор записи outbox. Используется для
	// подтверждения публикации и как event_id конверта.
	ID string
	// Event — сохранённое событие домена.
	Event domain.DomainEvent
}

// Source — журнал outbox со стороны публикации.
//
// Pending возвращает сообщения, ещё не подтверждённые получателем, в порядке
// записи. MarkPublished подтверждает успешную публикацию; до подтверждения
// сообщение считается недоставленным и будет выдано повторно.
type Source interface {
	Pending(ctx context.Context) ([]Message, error)
	MarkPublished(ctx context.Context, ids ...string) error
}

// Sink — назначение публикации.
//
// Реализация отвечает за доставку контракта во внешнюю систему. Она обязана
// быть идемпотентной по event_id: relay гарантирует доставку «как минимум один
// раз» и вправе повторить публикацию после падения между доставкой и
// подтверждением.
type Sink interface {
	Publish(ctx context.Context, event deliveryevent.DeliveryEvent) error
}

// Mapper преобразует событие домена в публикуемый контракт.
//
// Второе значение равно false, если событие не публикуется этим relay: оно
// подтверждается без публикации. Ошибка останавливает обход и не подтверждает
// текущее сообщение.
type Mapper func(event domain.DomainEvent) (deliveryevent.DeliveryEvent, bool, error)

// Relay публикует накопленные события из Source в Sink.
type Relay struct {
	source Source
	sink   Sink
	mapper Mapper
}

// New собирает relay с отображением delivery-event по умолчанию.
func New(source Source, sink Sink) *Relay {
	return NewWithMapper(source, sink, MapDeliveryEvent)
}

// NewWithMapper собирает relay с явным отображением событий.
func NewWithMapper(source Source, sink Sink, mapper Mapper) *Relay {
	return &Relay{source: source, sink: sink, mapper: mapper}
}

// PublishPending публикует все ожидающие сообщения в порядке записи и
// подтверждает каждое сразу после успешной публикации.
//
// Если публикация или подтверждение прервались, обход останавливается и
// возвращается ошибка; уже подтверждённые сообщения повторно не выдаются, а
// текущее и последующие будут доставлены при следующем вызове. Возвращённое
// число — сколько сообщений успешно опубликовано до остановки.
func (r *Relay) PublishPending(ctx context.Context) (int, error) {
	pending, err := r.source.Pending(ctx)
	if err != nil {
		return 0, fmt.Errorf("relay: чтение outbox: %w", err)
	}

	published := 0
	for _, message := range pending {
		event, ok, err := r.mapper(message.Event)
		if err != nil {
			return published, fmt.Errorf("relay: отображение события %s: %w", message.ID, err)
		}
		if !ok {
			// Событие не входит в публикуемый контракт среза: подтверждаем,
			// чтобы не выдавать его на каждом проходе.
			if err := r.source.MarkPublished(ctx, message.ID); err != nil {
				return published, fmt.Errorf("relay: подтверждение события %s: %w", message.ID, err)
			}
			continue
		}
		if event.EventID == "" {
			event.EventID = message.ID
		}
		// Публикуем только контракт, прошедший проверку обязательных полей:
		// невалидный контракт не доставляется и не подтверждается, чтобы ошибку
		// было видно, а не потерять её в чужой системе.
		if err := event.Validate(); err != nil {
			return published, fmt.Errorf("relay: проверка контракта события %s: %w", message.ID, err)
		}
		if err := r.sink.Publish(ctx, event); err != nil {
			return published, fmt.Errorf("relay: публикация события %s: %w", message.ID, err)
		}
		if err := r.source.MarkPublished(ctx, message.ID); err != nil {
			return published, fmt.Errorf("relay: подтверждение события %s: %w", message.ID, err)
		}
		published++
	}
	return published, nil
}

// MapDeliveryEvent отображает domain.MessageDelivered и domain.MessageDeliveryFailed
// в contracts.DeliveryEvent, а прочие события помечает как неподлежащие
// публикации.
//
// EventID намеренно не заполняется: его проставляет relay идентификатором записи
// outbox. CorrelationID — утверждённое действие как сквозной бизнес-процесс;
// CausationID не задаётся: событие доставки — корень своего факта.
func MapDeliveryEvent(event domain.DomainEvent) (deliveryevent.DeliveryEvent, bool, error) {
	switch e := event.(type) {
	case domain.MessageDelivered:
		out := deliveryevent.NewDeliveryEvent(deliveryevent.Envelope{
			OccurredAt:    e.At,
			TenantID:      e.TenantID.String(),
			CorrelationID: e.ApprovedActionID.String(),
		})
		out.MessageID = e.MessageID.String()
		out.ApprovedActionID = e.ApprovedActionID.String()
		out.CampaignID = e.CampaignID
		out.RecipientRef = e.RecipientRef.String()
		out.SenderDomain = e.SenderDomain.String()
		out.Outcome = deliveryevent.OutcomeDelivered
		out.AttemptNumber = int64(e.AttemptNumber)
		out.ProviderCode = e.ProviderCode
		return out, true, nil

	case domain.MessageDeliveryFailed:
		out := deliveryevent.NewDeliveryEvent(deliveryevent.Envelope{
			OccurredAt:    e.At,
			TenantID:      e.TenantID.String(),
			CorrelationID: e.ApprovedActionID.String(),
		})
		out.MessageID = e.MessageID.String()
		out.ApprovedActionID = e.ApprovedActionID.String()
		out.CampaignID = e.CampaignID
		out.RecipientRef = e.RecipientRef.String()
		out.SenderDomain = e.SenderDomain.String()
		out.Outcome = deliveryevent.OutcomeFailed
		out.AttemptNumber = int64(e.AttemptNumber)
		out.ProviderCode = e.ProviderCode
		return out, true, nil

	default:
		return deliveryevent.DeliveryEvent{}, false, nil
	}
}
