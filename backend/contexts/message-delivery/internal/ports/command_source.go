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

// ClaimedCommandSource предоставляет lease-команды с явным подтверждением
// результата обработчика. Реализация обязана возвращать истёкшие lease в очередь.
type ClaimedCommandSource interface {
	CommandSource
	Claim(ctx context.Context) (command domain.DeliveryCommand, token string, ok bool, err error)
	Ack(ctx context.Context, token string) error
	Release(ctx context.Context, token string) error
}

// DurableCommandQueue принимает payload по idempotency key и предоставляет
// атомарные lease для sender.
type DurableCommandQueue interface {
	Enqueue(ctx context.Context, command domain.DeliveryCommand) (duplicate bool, err error)
	Claim(ctx context.Context) (command domain.DeliveryCommand, token string, ok bool, err error)
	Ack(ctx context.Context, token string) error
	Release(ctx context.Context, token string) error
}
