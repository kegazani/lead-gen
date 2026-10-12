package domain

// MessageStatus — состояние доставки сообщения.
//
// Переходы: создано (pending) → отправляется (sending) → delivered, failed
// либо unknown. Любой другой переход запрещён. delivered, failed и unknown —
// терминальные состояния. Исключение — failed после временного исхода
// (недоступность провайдера): такое сообщение может быть возвращено в sending
// повторной попыткой (Message.ReopenForRetry), но не считается доставленным.
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

	// MessageStatusUnknown — SMTP мог принять письмо, но acknowledgement потерян.
	// Автоматическая повторная отправка запрещена до ручной сверки.
	MessageStatusUnknown MessageStatus = "unknown"
)

// IsTerminal сообщает, что сообщение вышло из жизненного цикла доставки.
func (s MessageStatus) IsTerminal() bool {
	switch s {
	case MessageStatusDelivered, MessageStatusFailed, MessageStatusUnknown:
		return true
	default:
		return false
	}
}

// isValid сообщает, что состояние объявлено доменом.
func (s MessageStatus) isValid() bool {
	switch s {
	case MessageStatusPending, MessageStatusSending, MessageStatusDelivered, MessageStatusFailed, MessageStatusUnknown:
		return true
	default:
		return false
	}
}
