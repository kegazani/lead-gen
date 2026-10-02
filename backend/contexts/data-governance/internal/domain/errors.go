package domain

import "errors"

// Доменные ошибки контекста data-governance.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingActor — изменение не назвало актора.
	ErrMissingActor = errors.New("домен: изменение без актора")

	// ErrMissingTime — изменение не назвало время.
	ErrMissingTime = errors.New("домен: изменение без времени")

	// ErrUnknownCategory — категория данных не объявлена доменом.
	ErrUnknownCategory = errors.New("домен: неизвестная категория данных")

	// ErrEmptyPurpose — цель обработки пуста после обрезки пробелов.
	ErrEmptyPurpose = errors.New("домен: пустая цель обработки")

	// ErrInvalidRetention — срок хранения не положителен или превышает предел.
	ErrInvalidRetention = errors.New("домен: некорректный срок хранения")

	// ErrInvalidValidityWindow — окно действия полномочия задано противоречиво.
	ErrInvalidValidityWindow = errors.New("домен: некорректное окно действия полномочия")

	// ErrBasisRevoked — полномочие отозвано и не меняется.
	ErrBasisRevoked = errors.New("домен: полномочие отозвано")

	// ErrReasonRequired — изменение (отзыв, снятие стоп-листа) требует причины.
	ErrReasonRequired = errors.New("домен: причина обязательна")

	// ErrAlreadySuppressed — активная запись стоп-листа для контакта уже есть.
	ErrAlreadySuppressed = errors.New("домен: контакт уже в стоп-листе")

	// ErrSuppressionNotFound — запись стоп-листа не найдена в агрегате.
	ErrSuppressionNotFound = errors.New("домен: запись стоп-листа не найдена")

	// ErrSuppressionNotActive — попытка снять уже неактивную запись стоп-листа.
	ErrSuppressionNotActive = errors.New("домен: запись стоп-листа неактивна")

	// ErrInvalidSnapshot — сохранённое состояние внутренне противоречиво и не
	// может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние")
)
