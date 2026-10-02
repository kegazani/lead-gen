package platform

import (
	"context"

	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Unavailable — официальный интерфейс площадок не подключён: порт честно
// отвечает отказом, а не выдумывает адрес публикации. Так выглядит сборка, пока
// адаптер интеграционной платформы (provider-port) не подключён.
type Unavailable struct{}

// Unavailable реализует порт публикации.
var _ ports.PlatformPublisher = Unavailable{}

// Ready реализует ports.PlatformPublisher: канал без доступа не готов.
func (Unavailable) Ready(context.Context, string, string) error {
	return ports.ErrChannelNotReady
}

// Publish реализует ports.PlatformPublisher: публикация недоступна.
func (Unavailable) Publish(context.Context, ports.PublishRequest) (ports.PublishResult, error) {
	return ports.PublishResult{}, ports.ErrPlatformUnavailable
}
