package domain

// MessageStatus — состояние доставки сообщения.
//
// Переходы: создано (pending) → отправляется (sending) → доставлено (delivered)
// либо отказ (failed). Любой другой переход запрещён. delivered и failed —
// терминальные состояния: повторная отправка сообщения не выполняется.
type MessageStatus string

const (
	// MessageStatusPending — сообщение создано и ожидает отправки.
	MessageStatusPending MessageStatus = "pending"

	// MessageStatusSending — сообщение передано провайдеру; исход ещё не получен.
	MessageStatusSending MessageStatus = "sending"

	// MessageStatusDelivered — провайдер принял сообщение.
	MessageStatusDelivered MessageStatus = "delivered"

	// MessageStatusFailed — доставка не состоялась.
	MessageStatusFailed MessageStatus = "failed"
)

// IsTerminal сообщает, что сообщение вышло из жизненного цикла доставки.
func (s MessageStatus) IsTerminal() bool {
	switch s {
	case MessageStatusDelivered, MessageStatusFailed:
		return true
	default:
		return false
	}
}

// isValid сообщает, что состояние объявлено доменом.
func (s MessageStatus) isValid() bool {
	switch s {
	case MessageStatusPending, MessageStatusSending, MessageStatusDelivered, MessageStatusFailed:
		return true
	default:
		return false
	}
}
