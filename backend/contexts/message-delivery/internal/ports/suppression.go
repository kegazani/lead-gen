package ports

import (
	"context"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

// SuppressionList — стоп-лист получателей (владение data-governance, contract
// suppression-policy).
//
// Проверка стоп-листа выполняется в сценарии доставки непосредственно перед
// попыткой: активный стоп-лист блокирует отправку независимо от того, была ли
// она допустима раньше (инвариант message).
type SuppressionList interface {
	// IsSuppressed сообщает, заблокирован ли адрес назначения. Ошибка означает
	// недоступность стоп-листа, а не «не заблокирован»: вызывающий не вправе
	// отправить письмо при неуверенности.
	IsSuppressed(ctx context.Context, address domain.DeliveryAddress) (bool, error)
}
