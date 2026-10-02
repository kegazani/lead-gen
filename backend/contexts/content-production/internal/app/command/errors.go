package command

import "errors"

// Ошибки прикладного слоя контекста content-production.
var (
	// ErrTenantMismatch — контент-пакет принадлежит другому тенанту. Сценарий не
	// раскрывает существование чужого пакета: вызывающий получает отказ.
	ErrTenantMismatch = errors.New("сценарий: контент-пакет принадлежит другому тенанту")

	// ErrVersionRequired — запрошен рендер версии, которой нет в пакете.
	ErrVersionRequired = errors.New("сценарий: версия контента не найдена в пакете")

	// ErrPackageRequired — производство контента без текста не запускается: пакет
	// не из чего создать.
	ErrPackageRequired = errors.New("сценарий: для нового пакета нужен текст контента")
)
