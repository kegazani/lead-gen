// Package adaptercapability — опубликованный (published language) контракт
// adapter-capability контекста integration-platform.
//
// Контракт объявляет возможности адаптера: какие операции адаптер умеет
// исполнять и какая версия набора возможностей за это отвечает. Объявление
// передаётся в общем конверте (Envelope) по форме docs/domain-model.md.
//
// Потребитель (например, lead-management) сверяет объявленные возможности со
// своими обязательными до вызова: недостающая возможность — это отказ до
// начала обработки, а не ошибка во время исполнения (design.md, Решение 2).
//
// Зависимости — исключительно стандартная библиотека: контракт не импортирует
// SDK, модули контекстов и platform.
package adaptercapability

import (
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта.
const ContractName = "adapter-capability"

// SchemaVersion — текущая версия схемы контракта (semver). Изменять по правилам
// semver из envelope.go.
const SchemaVersion = "1.0.0"

// Capability — типизированное объявление одной возможности адаптера.
type Capability struct {
	// Name — стабильное имя возможности. Обязательно.
	Name string `json:"name"`
	// SetVersion — версия набора возможностей, к которой относится объявление,
	// в формате semver. Обязательна. Меняется при совместимом или несовместимом
	// изменении состава набора, тогда как SchemaVersion отвечает за форму
	// контракта.
	SetVersion string `json:"set_version"`
}

// Validate проверяет обязательные поля возможности и называет все нарушения разом.
func (c Capability) Validate() error {
	var invalid []string
	if strings.TrimSpace(c.Name) == "" {
		invalid = append(invalid, "capabilities.name")
	}
	if !IsValidSemver(c.SetVersion) {
		invalid = append(invalid, "capabilities.set_version")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("adaptercapability: capability: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Declaration — публикуемое объявление набора возможностей адаптера. Собирается
// helper-функцией NewDeclaration и передаётся в конверте.
type Declaration struct {
	Envelope

	// AdapterName — имя адаптера, объявляющего возможности. Обязательно.
	AdapterName string `json:"adapter_name"`
	// Capabilities — объявленный набор возможностей. Непустой.
	Capabilities []Capability `json:"capabilities"`
}

// NewDeclaration возвращает объявление с проставленными именем и версией схемы
// контракта. Остальные поля заполняет вызывающий.
func NewDeclaration(env Envelope) Declaration {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return Declaration{Envelope: env}
}

// Validate проверяет конверт и обязательные поля объявления. Ошибка называет
// все отсутствующие или некорректные поля. Набор возможностей не может быть
// пустым, а каждая возможность проверяется на имя и версию набора.
func (d Declaration) Validate() error {
	if err := d.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if strings.TrimSpace(d.AdapterName) == "" {
		invalid = append(invalid, "adapter_name")
	}
	if len(d.Capabilities) == 0 {
		invalid = append(invalid, "capabilities")
	}
	for i, capability := range d.Capabilities {
		if err := capability.Validate(); err != nil {
			invalid = append(invalid, fmt.Sprintf("capabilities[%d]", i))
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("adaptercapability: adapter-capability: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}
