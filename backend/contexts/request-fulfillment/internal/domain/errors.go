package domain

import "errors"

// Доменные ошибки контекста request-fulfillment.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingActor — переход статуса не назвал актора.
	ErrMissingActor = errors.New("домен: переход статуса без актора")

	// ErrMissingTime — переход статуса не назвал время.
	ErrMissingTime = errors.New("домен: переход статуса без времени")

	// ErrMissingEventID — событие домена без идентификатора: его нельзя
	// дедуплицировать у потребителя.
	ErrMissingEventID = errors.New("домен: событие без идентификатора")

	// ErrUnknownRequestStatus — статус заявки не объявлен доменом.
	ErrUnknownRequestStatus = errors.New("домен: неизвестный статус заявки")

	// ErrInvalidTransition — переход статуса запрещён статусной моделью.
	ErrInvalidTransition = errors.New("домен: недопустимый переход статуса заявки")

	// ErrOfferRequired — заявка без оффера не создаётся.
	ErrOfferRequired = errors.New("домен: заявка без оффера не создаётся")

	// ErrEmptyOfferContent — текст оффера пуст после обрезки пробелов.
	ErrEmptyOfferContent = errors.New("домен: текст оффера пуст")

	// ErrTenantImmutable — попытка изменить тенант заявки.
	ErrTenantImmutable = errors.New("домен: тенант заявки неизменяем")

	// ErrTerminalState — заявка находится в терминальном состоянии и не меняется.
	ErrTerminalState = errors.New("домен: заявка в терминальном состоянии")

	// ErrReasonRequired — переход (отклонение, отмена) требует причины.
	ErrReasonRequired = errors.New("домен: причина перехода обязательна")

	// ErrInvalidOfferRevision — сохранённая версия оффера некорректна.
	ErrInvalidOfferRevision = errors.New("домен: некорректная версия оффера")

	// ErrInvalidSnapshot — сохранённое состояние заявки внутренне противоречиво
	// и не может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние заявки")

	// ErrUnknownValidationOutcome — исход валидации не объявлен доменом.
	ErrUnknownValidationOutcome = errors.New("домен: неизвестный исход валидации")

	// ErrCallIDRequired — ссылка на звонок пуста.
	ErrCallIDRequired = errors.New("домен: пустой идентификатор звонка")

	// ErrEmptyEmbedding — попытка зафиксировать пустой вектор эмбеддинга.
	ErrEmptyEmbedding = errors.New("домен: пустой вектор эмбеддинга")
)
