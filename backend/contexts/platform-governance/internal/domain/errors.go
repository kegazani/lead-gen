package domain

import "errors"

// Доменные ошибки контекста platform-governance.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingTime — событие домена без времени: его нельзя упорядочить.
	ErrMissingTime = errors.New("домен: отсутствует время")

	// ErrCompanyNameRequired — название компании пусто.
	ErrCompanyNameRequired = errors.New("домен: название компании обязательно")

	// ErrDescriptionRequired — описание компании пусто.
	ErrDescriptionRequired = errors.New("домен: описание компании обязательно")

	// ErrUserLoginRequired — логин пользователя пуст.
	ErrUserLoginRequired = errors.New("домен: логин пользователя обязателен")

	// ErrUnknownRole — роль не объявлена доменом.
	ErrUnknownRole = errors.New("домен: неизвестная роль")

	// ErrDuplicateUser — пользователь уже принадлежит тенанту. Инвариант: роль
	// принадлежит ровно одному тенанту.
	ErrDuplicateUser = errors.New("домен: пользователь уже принадлежит тенанту")

	// ErrInvalidSnapshot — сохранённое состояние тенанта внутренне противоречиво
	// и не может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние тенанта")
)
