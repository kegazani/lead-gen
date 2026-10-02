package ports

import (
	"context"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

// CommandSource — источник входящих команд доставки для роли sender.
//
// Поток команд отвязан от конкретного транспорта: в совмещённом процессе это
// очередь в памяти, при разделении сервисов — подписка на очередь брокера или
// HTTP-приём. Next блокируется до появления команды, закрытия источника или
// отмены контекста.
type CommandSource interface {
	// Next возвращает следующую команду. Второе значение равно false, если
	// источник закрыт: обработка завершается штатно. Отмена контекста даёт
	// ошибку, для которой истинно errors.Is(err, context.Canceled) или
	// errors.Is(err, context.DeadlineExceeded).
	Next(ctx context.Context) (command domain.DeliveryCommand, ok bool, err error)
}
