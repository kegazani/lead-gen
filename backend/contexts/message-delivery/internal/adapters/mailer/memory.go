package mailer

import (
	"context"
	"sync"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// defaultProviderCode — нормализованный код успешного ответа, если вызывающий не
// задал свой.
const defaultProviderCode = "250"

// Memory — in-memory реализация ports.Mailer для сборки и тестов.
//
// Провайдер не вызывается: адаптер записывает отправленные сообщения и отвечает
// заранее заданным нормализованным ответом. По умолчанию ответ — «доставлено»;
// тест может задать исход для конкретного получателя или общий исход. Адаптер
// безопасен для конкурентного использования.
type Memory struct {
	mu             sync.Mutex
	defaultOutcome ports.ProviderResponse
	byRecipient    map[domain.RecipientRef]ports.ProviderResponse
	sent           []ports.OutgoingMessage
}

// Memory реализует порт отправки почты.
var _ ports.Mailer = (*Memory)(nil)

// New создаёт адаптер с ответом по умолчанию «доставлено».
func New() *Memory {
	return &Memory{
		defaultOutcome: ports.ProviderResponse{Outcome: domain.OutcomeDelivered, ProviderCode: defaultProviderCode},
		byRecipient:    make(map[domain.RecipientRef]ports.ProviderResponse),
	}
}

// Respond задаёт общий ответ на все отправки. Возвращает адаптер для цепочки
// вызовов при конфигурации.
func (m *Memory) Respond(response ports.ProviderResponse) *Memory {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultOutcome = response
	return m
}

// RespondTo задаёт ответ для конкретного получателя, переопределяя общий.
func (m *Memory) RespondTo(ref domain.RecipientRef, response ports.ProviderResponse) *Memory {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byRecipient[ref] = response
	return m
}

// Send реализует ports.Mailer. Провайдер не вызывается: сообщение записывается, а
// ответ берётся из настроек адаптера.
func (m *Memory) Send(_ context.Context, message ports.OutgoingMessage) (ports.ProviderResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, message)
	if response, ok := m.byRecipient[message.RecipientRef]; ok {
		return response, nil
	}
	return m.defaultOutcome, nil
}

// Sent возвращает копию отправленных сообщений в порядке отправки. Метод нужен
// проверкам и диагностике; на обработку он не влияет.
func (m *Memory) Sent() []ports.OutgoingMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ports.OutgoingMessage, len(m.sent))
	copy(out, m.sent)
	return out
}
