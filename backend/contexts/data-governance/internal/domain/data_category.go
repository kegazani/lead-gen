package domain

import "fmt"

// DataCategory — категория (зона) данных, к которой относится обработка.
//
// Набор совпадает с зонами владения данными из docs/domain-model.md («Владение и
// зоны данных»): green, mixed, red, analytics, external. Категория — часть
// value object processing-purpose: полномочие действительно только для указанной
// цели вместе с категорией (инвариант I1).
type DataCategory string

const (
	// CategoryGreen — данные, допустимые в платформенном хранилище.
	CategoryGreen DataCategory = "green"

	// CategoryMixed — данные, разделяемые по атрибутам: green-часть в платформе,
	// red-часть на стороне клиента.
	CategoryMixed DataCategory = "mixed"

	// CategoryRed — данные, хранимые только в клиентском хранилище.
	CategoryRed DataCategory = "red"

	// CategoryAnalytics — обезличенные/агрегированные производные.
	CategoryAnalytics DataCategory = "analytics"

	// CategoryExternal — внешний system of record: локально только ссылка.
	CategoryExternal DataCategory = "external"
)

// DataCategories возвращает согласованный набор категорий данных.
func DataCategories() []DataCategory {
	return []DataCategory{CategoryGreen, CategoryMixed, CategoryRed, CategoryAnalytics, CategoryExternal}
}

// isValid сообщает, что категория объявлена доменом.
func (c DataCategory) isValid() bool {
	switch c {
	case CategoryGreen, CategoryMixed, CategoryRed, CategoryAnalytics, CategoryExternal:
		return true
	default:
		return false
	}
}

// validate отвергает категорию вне согласованного набора.
func (c DataCategory) validate() error {
	if !c.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownCategory, c)
	}
	return nil
}

// ClientSideOnly сообщает, что категория обрабатывается исключительно на стороне
// клиента: red-данные не выносятся в общее хранилище, логи, события и аналитику
// (docs/domain-model.md, правило 3).
func (c DataCategory) ClientSideOnly() bool { return c == CategoryRed }
