// Package outbox — transactional outbox и relay публикации после commit.
//
// Изменение агрегата и запись сообщения выполняются в одной транзакции владельца;
// публикация следует за успешным commit и может повториться, если доставка или
// подтверждение прервались (design.md, Решения 3 и 4). Так «изменение сохранено,
// но событие не ушло» и «событие ушло, но изменение откатилось» становятся
// невозможными: в брокер попадает только зафиксированное сообщение.
//
// Relay не знает ни о брокере, ни о базе: он читает ожидающие записи из Journal
// и отдаёт готовое сообщение в Publisher. Гарантия доставки — at-least-once;
// повтор не создаёт второго эффекта, потому что потребитель дедуплицирует по
// event_id (platform/messaging/inbox).
package outbox

import (
	"context"
)

// Message — запись outbox, ожидающая публикации.
type Message struct {
	// ID — стабильный идентификатор записи. Служит ключом идемпотентности
	// публикации: одна и та же запись публикуется не более одного раза.
	ID string
	// Exchange — topic exchange контекста-владельца.
	Exchange string
	// RoutingKey — ключ маршрутизации <contract>.<major>.
	RoutingKey string
	// Body — тело сообщения (например, JSON контракта).
	Body []byte
	// Headers — метаданные конверта, включая event_id.
	Headers map[string]string
}

// Journal — журнал outbox со стороны публикации.
//
// Append вызывается владельцем в его транзакции. Pending возвращает ещё не
// опубликованные записи в порядке записи. MarkPublished подтверждает успешную
// публикацию; до подтверждения запись считается недоставленной и будет выдана
// повторно при следующем проходе или после рестарта.
type Journal interface {
	Append(ctx context.Context, m Message) error
	Pending(ctx context.Context, limit int) ([]Message, error)
	MarkPublished(ctx context.Context, id string) error
}

// Publisher — назначение публикации.
//
// Реализация отвечает за доставку сообщения в брокер и обязана подтверждать
// публикацию (publisher confirms): nil означает «брокер принял», ошибка — «не
// принял, повтори». Повторную доставку разбирает потребитель дедупликацией по
// event_id.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte, headers map[string]string) error
}
