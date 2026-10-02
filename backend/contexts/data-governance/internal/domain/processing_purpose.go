package domain

import (
	"strings"
)

// ProcessingPurpose — value object: цель обработки данных вместе с категорией
// (зоной), к которой относится обработка.
//
// Полномочие действительно только для указанной цели и категории (инвариант I1),
// поэтому цель и категория неразделимы в одном VO. Значение неизменяемо и
// валидируется при создании.
type ProcessingPurpose struct {
	name     string
	category DataCategory
}

// NewProcessingPurpose создаёт цель обработки. Имя обрезается по пробелам и не
// может быть пустым; категория обязана быть объявленной.
func NewProcessingPurpose(name string, category DataCategory) (ProcessingPurpose, error) {
	purpose := ProcessingPurpose{name: strings.TrimSpace(name), category: category}
	if err := purpose.validate(); err != nil {
		return ProcessingPurpose{}, err
	}
	return purpose, nil
}

// Name возвращает имя цели обработки.
func (p ProcessingPurpose) Name() string { return p.name }

// Category возвращает категорию (зону) данных цели.
func (p ProcessingPurpose) Category() DataCategory { return p.category }

// String возвращает имя цели.
func (p ProcessingPurpose) String() string { return p.name }

// Equal сравнивает цели по имени и категории.
func (p ProcessingPurpose) Equal(other ProcessingPurpose) bool {
	return p.name == other.name && p.category == other.category
}

// validate проверяет имя и категорию.
func (p ProcessingPurpose) validate() error {
	if p.name == "" {
		return ErrEmptyPurpose
	}
	return p.category.validate()
}

// String реализует fmt.Stringer для словаря категорий.
func (c DataCategory) String() string { return string(c) }
