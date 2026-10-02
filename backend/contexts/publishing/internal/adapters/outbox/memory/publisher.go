// Package memory реализует приёмник публикации outbox в оперативной памяти: он
// запоминает доставленные сообщения вместо отправки в брокер. Годится для среза и
// тестов, где реальный брокер не подключается.
package memory

import (
	"context"
	"sync"

	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Publisher — in-memory реализация platformoutbox.Publisher.
type Publisher struct {
	mu       sync.Mutex
	messages []platformoutbox.Message
}

// Publisher реализует приёмник публикации outbox.
var _ platformoutbox.Publisher = (*Publisher)(nil)

// NewPublisher создаёт пустой приёмник.
func NewPublisher() *Publisher { return &Publisher{} }

// Publish реализует platformoutbox.Publisher: сообщение принимается и
// запоминается, подтверждая доставку.
func (p *Publisher) Publish(_ context.Context, exchange, routingKey string, body []byte, headers map[string]string) error {
	copiedHeaders := make(map[string]string, len(headers))
	for k, v := range headers {
		copiedHeaders[k] = v
	}
	copiedBody := make([]byte, len(body))
	copy(copiedBody, body)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, platformoutbox.Message{
		Exchange:   exchange,
		RoutingKey: routingKey,
		Body:       copiedBody,
		Headers:    copiedHeaders,
	})
	return nil
}

// Messages возвращает копию доставленных сообщений.
func (p *Publisher) Messages() []platformoutbox.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]platformoutbox.Message, len(p.messages))
	copy(out, p.messages)
	return out
}
