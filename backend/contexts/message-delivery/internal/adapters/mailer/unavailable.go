package mailer

import (
	"context"

	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Unavailable — провайдер почты не подключён: порт честно отвечает отказом, а не
// выдумывает исход доставки. Так выглядит сборка, пока контракт provider-port
// (integration-platform) не подключён.
type Unavailable struct{}

// Unavailable реализует порт отправки почты.
var _ ports.Mailer = Unavailable{}

// Send реализует ports.Mailer.
func (Unavailable) Send(context.Context, ports.OutgoingMessage) (ports.ProviderResponse, error) {
	return ports.ProviderResponse{}, ports.ErrMailerUnavailable
}
