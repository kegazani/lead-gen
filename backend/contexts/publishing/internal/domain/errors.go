package domain

import "errors"

// Доменные ошибки контекста publishing.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingActor — действие не назвало актора.
	ErrMissingActor = errors.New("домен: действие без актора")

	// ErrMissingTime — действие не назвало время.
	ErrMissingTime = errors.New("домен: действие без времени")

	// ErrMissingEventID — событие домена без идентификатора: его нельзя
	// дедуплицировать у потребителя.
	ErrMissingEventID = errors.New("домен: событие без идентификатора")

	// ErrUnknownPlatform — площадка не объявлена доменом.
	ErrUnknownPlatform = errors.New("домен: неизвестная площадка публикации")

	// ErrEmptyAccount — не названа учётная запись площадки.
	ErrEmptyAccount = errors.New("домен: пустая учётная запись площадки")

	// ErrEmptyMaterial — текст материала пуст после обрезки пробелов.
	ErrEmptyMaterial = errors.New("домен: пустой материал публикации")

	// ErrDigestRequired — публикация без ссылки на выжимку-источник не создаётся.
	ErrDigestRequired = errors.New("домен: публикация без источника-выжимки")

	// ErrUnknownStatus — статус публикации не объявлен доменом.
	ErrUnknownStatus = errors.New("домен: неизвестный статус публикации")

	// ErrInvalidTransition — переход статуса запрещён статусной моделью.
	ErrInvalidTransition = errors.New("домен: недопустимый переход статуса публикации")

	// ErrTerminalState — публикация находится в терминальном состоянии и не
	// меняется.
	ErrTerminalState = errors.New("домен: публикация в терминальном состоянии")

	// ErrApprovalRequired — публикация без утверждения человека не исполняется
	// (policy approved-publication-only).
	ErrApprovalRequired = errors.New("домен: публикация не утверждена")

	// ErrReasonRequired — переход (отклонение, отказ площадки) требует причины.
	ErrReasonRequired = errors.New("домен: причина перехода обязательна")

	// ErrAddressRequired — успешная публикация обязана нести адрес материала.
	ErrAddressRequired = errors.New("домен: адрес публикации обязателен")

	// ErrInvalidSnapshot — сохранённое состояние публикации внутренне
	// противоречиво и не может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние публикации")

	// ErrDuplicateKeyInBatch — в одной команде два материала претендуют на один
	// ключ идемпотентности.
	ErrDuplicateKeyInBatch = errors.New("домен: повтор ключа идемпотентности в команде")
)
