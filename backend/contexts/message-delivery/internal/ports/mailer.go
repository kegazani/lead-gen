package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

// ErrMailerUnavailable — почтовый провайдер недоступен. Сценарий различает это
// от доменного отказа: недоступность — временный сбой транспорта, а не решение
// о недопустимости отправки.
var ErrMailerUnavailable = errors.New("порты: почтовый провайдер недоступен")

// OutgoingMessage — сообщение, передаваемое провайдеру. Содержит минимум,
// необходимый порту отправки: адрес назначения, отправляющий домен и
// идентификаторы для трассировки. Тела письма здесь нет: сборку содержимого
// выполняет интеграция с content-production, в объём этого среза не входящая.
type OutgoingMessage struct {
	MessageID    domain.MessageID
	TenantID     domain.TenantID
	SenderDomain domain.SenderDomain
	To           domain.DeliveryAddress
	RecipientRef domain.RecipientRef
}

// ProviderResponse — нормализованный ответ провайдера: исход и код.
type ProviderResponse struct {
	// Outcome — нормализованный исход попытки.
	Outcome domain.AttemptOutcome

	// ProviderCode — нормализованный код ответа (необязателен).
	ProviderCode string
}

// Mailer — порт отправки почты (SMTP за портом).
//
// Реализация отвечает за сетевой обмен с провайдером и обязана вернуть
// нормализованный ответ. Ошибка означает недоступность провайдера; вызывающий
// трактует её как временный сбой, а не как решение провайдера о доставке.
type Mailer interface {
	Send(ctx context.Context, message OutgoingMessage) (ProviderResponse, error)
}
