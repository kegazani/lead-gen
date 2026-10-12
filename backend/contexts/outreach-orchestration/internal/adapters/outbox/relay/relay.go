// Package relay публикует события transactional outbox после commit.
//
// Изменение агрегата и запись события action-approved выполняются в одной
// транзакции; публикация следует за успешным commit и может повториться, если
// доставка или подтверждение прервались (design.md, Решение 4). Relay не
// знает о брокере и базе: он читает ожидающие сообщения из Source и отдаёт
// готовый контракт в Sink. Повторная доставка не создаёт второго внешнего
// эффекта, потому что получатель идемпотентен по ключу
// (tenant_id, campaign_id, draft_id, draft_version) —
// contracts.ApprovedAction.IdempotencyKey.
//
// Интерфейсы Source и Sink объявлены здесь намеренно: назначение публикации
// задаётся в рамках среза, без подключения реальной очереди или внешней
// системы.
package relay

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
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
// быть идемпотентной по contracts.ApprovedAction.IdempotencyKey: relay
// гарантирует доставку «как минимум один раз» и вправе повторить публикацию
// после падения между доставкой и подтверждением.
type Sink interface {
	Publish(ctx context.Context, action contracts.ApprovedAction) error
}

// Mapper преобразует событие домена в публикуемый контракт.
//
// Второе значение равно false, если событие не публикуется этим relay: оно
// подтверждается без публикации. Ошибка останавливает обход и не подтверждает
// текущее сообщение.
type Mapper func(event domain.DomainEvent) (action contracts.ApprovedAction, ok bool, err error)

// Relay публикует накопленные события из Source в Sink.
type Relay struct {
	source Source
	sink   Sink
	mapper Mapper
}

// New собирает relay с отображением approved-action по умолчанию.
func New(source Source, sink Sink) *Relay {
	return NewWithMapper(source, sink, MapActionApproved)
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
		action, ok, err := r.mapper(message.Event)
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
		if action.EventID == "" {
			action.EventID = message.ID
		}
		// Публикуем только контракт, прошедший проверку обязательных полей:
		// невалидный контракт не доставляется и не подтверждается, чтобы ошибку
		// было видно, а не потерять её в чужой системе. Проверка внутренняя:
		// помимо формы контракта она сверяет канал с доменным VO action-channel.
		if err := contracts.ValidateApprovedAction(action); err != nil {
			return published, fmt.Errorf("relay: проверка контракта события %s: %w", message.ID, err)
		}
		if err := r.sink.Publish(ctx, action); err != nil {
			return published, fmt.Errorf("relay: публикация события %s: %w", message.ID, err)
		}
		if err := r.source.MarkPublished(ctx, message.ID); err != nil {
			return published, fmt.Errorf("relay: подтверждение события %s: %w", message.ID, err)
		}
		published++
	}
	return published, nil
}

// MapActionApproved отображает domain.ActionApproved в contracts.ApprovedAction,
// а прочие события помечает как неподлежащие публикации.
//
// Отображение задано один раз — contracts.FromActionApproved — и используется как
// публикацией, так и чтением решения по черновику. EventID намеренно не заполняется:
// его проставляет relay идентификатором записи outbox.
func MapActionApproved(event domain.DomainEvent) (contracts.ApprovedAction, bool, error) {
	approved, ok := event.(domain.ActionApproved)
	if !ok {
		return contracts.ApprovedAction{}, false, nil
	}
	return contracts.FromActionApproved(approved), true, nil
}
