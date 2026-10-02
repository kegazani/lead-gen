package domain

import "errors"

// Доменные ошибки контекста content-production.
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

	// ErrEmptyContent — текст контента пуст после обрезки пробелов.
	ErrEmptyContent = errors.New("домен: текст контента пуст")

	// ErrInvalidLocale — язык или регион контента заданы недопустимо.
	ErrInvalidLocale = errors.New("домен: некорректная локаль контента")

	// ErrUnknownRenderFormat — формат рендера не объявлен доменом.
	ErrUnknownRenderFormat = errors.New("домен: неизвестный формат рендера")

	// ErrTemplateRequired — рендер без шаблона не создаётся.
	ErrTemplateRequired = errors.New("домен: шаблон рендера обязателен")

	// ErrVersionRequired — рендер без точной версии контента не создаётся.
	ErrVersionRequired = errors.New("домен: версия контента для рендера обязательна")

	// ErrUnknownRenderState — состояние задания рендеринга не объявлено доменом.
	ErrUnknownRenderState = errors.New("домен: неизвестное состояние задания рендеринга")

	// ErrInvalidRenderTransition — переход состояния задания запрещён моделью.
	ErrInvalidRenderTransition = errors.New("домен: недопустимый переход состояния рендера")

	// ErrTerminalRenderState — задание рендеринга в терминальном состоянии.
	ErrTerminalRenderState = errors.New("домен: задание рендеринга завершено")

	// ErrReasonRequired — отказ рендера требует причины.
	ErrReasonRequired = errors.New("домен: причина отказа рендера обязательна")

	// ErrArtifactRequired — завершение рендера без артефакта недопустимо.
	ErrArtifactRequired = errors.New("домен: артефакт рендера обязателен")

	// ErrArtifactFormatMismatch — артефакт не соответствует формату задания.
	ErrArtifactFormatMismatch = errors.New("домен: формат артефакта не совпадает с заданием")

	// ErrVersionNotFound — ссылка на версию не найдена в контент-пакете.
	ErrVersionNotFound = errors.New("домен: версия контента не найдена")

	// ErrTenantMismatch — пакет принадлежит другому тенанту.
	ErrTenantMismatch = errors.New("домен: пакет принадлежит другому тенанту")

	// ErrInvalidSnapshot — сохранённое состояние внутренне противоречиво и не
	// может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние")
)
