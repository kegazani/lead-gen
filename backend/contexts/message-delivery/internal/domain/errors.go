package domain

import "errors"

// Доменные ошибки контекста message-delivery.
//
// Ошибка не описывает транспорт или хранилище: это нарушение правила домена.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены как
// именованные значения, а не как текст.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrEmptyDeliveryAddress — адрес назначения не задан.
	ErrEmptyDeliveryAddress = errors.New("домен: адрес назначения не задан")

	// ErrInvalidDeliveryAddress — адрес назначения не похож на почтовый.
	ErrInvalidDeliveryAddress = errors.New("домен: некорректный адрес назначения")

	// ErrEmptySenderDomain — отправляющий домен не задан.
	ErrEmptySenderDomain = errors.New("домен: отправляющий домен не задан")

	// ErrInvalidSenderDomain — отправляющий домен некорректен.
	ErrInvalidSenderDomain = errors.New("домен: некорректный отправляющий домен")

	// ErrIdempotencyKeyRequired — сообщение создано без ключа идемпотентности;
	// без него повторная отправка неотличима от первой.
	ErrIdempotencyKeyRequired = errors.New("домен: ключ идемпотентности сообщения не задан")

	// ErrApprovedActionRequired — доставка без утверждённого действия
	// (политика approved-action-only).
	ErrApprovedActionRequired = errors.New("домен: доставка возможна только для утверждённого действия")

	// ErrInvalidMessageStatus — состояние сообщения не объявлено доменом.
	ErrInvalidMessageStatus = errors.New("домен: недопустимое состояние сообщения")

	// ErrInvalidMessageTransition — переход состояния сообщения запрещён его
	// текущим состоянием.
	ErrInvalidMessageTransition = errors.New("домен: недопустимый переход состояния сообщения")

	// ErrUnknownAttemptOutcome — исход попытки не объявлен доменом.
	ErrUnknownAttemptOutcome = errors.New("домен: неизвестный исход попытки доставки")

	// ErrAttemptTimeRequired — время попытки не задано.
	ErrAttemptTimeRequired = errors.New("домен: время попытки доставки не задано")

	// ErrInvalidAttemptNumber — номер попытки отсутствует или не положителен.
	ErrInvalidAttemptNumber = errors.New("домен: недопустимый номер попытки доставки")

	// ErrSenderSuspended — новые отправки домена приостановлены активным
	// ограничением репутации.
	ErrSenderSuspended = errors.New("домен: отправки домена приостановлены")

	// ErrRecipientSuppressed — активный стоп-лист блокирует попытку доставки.
	ErrRecipientSuppressed = errors.New("домен: получатель в стоп-листе")

	// ErrInvalidSnapshot — сохранённое состояние агрегата внутренне
	// противоречиво и не может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние агрегата")
)
