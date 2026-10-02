package domain

import "errors"

// Доменные ошибки контекста sales-workflow.
//
// Ошибка описывает нарушение правила домена, а не сбой хранилища или транспорта.
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

	// ErrUnknownDealStage — этап сделки не объявлен доменом.
	ErrUnknownDealStage = errors.New("домен: неизвестный этап сделки")

	// ErrInvalidStageTransition — переход этапа запрещён согласованным workflow.
	ErrInvalidStageTransition = errors.New("домен: недопустимый переход этапа сделки")

	// ErrTerminalDeal — сделка в терминальном этапе: переходов из него нет.
	ErrTerminalDeal = errors.New("домен: сделка в терминальном этапе")

	// ErrInvalidExternalVersion — внешняя версия записи CRM некорректна.
	ErrInvalidExternalVersion = errors.New("домен: некорректная внешняя версия")

	// ErrInsufficientProof — доказательство атрибуции неполно: подтверждённый
	// результат обязан иметь проверяемое основание и идемпотентную внешнюю ссылку.
	ErrInsufficientProof = errors.New("домен: недостаточное доказательство атрибуции")

	// ErrAlreadyAttributed — результат уже подтверждён другой внешней ссылкой.
	ErrAlreadyAttributed = errors.New("домен: результат уже атрибутирован")

	// ErrAttributionNotAllowed — этап сделки не допускает подтверждение результата.
	ErrAttributionNotAllowed = errors.New("домен: атрибуция недопустима на текущем этапе")

	// ErrInvalidSnapshot — сохранённое состояние агрегата повреждено.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние")
)
