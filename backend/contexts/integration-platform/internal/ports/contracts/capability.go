package contracts

import (
	"fmt"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/contracts/integration-platform/adaptercapability"
)

// Реэкспорт опубликованного контракта adapter-capability.

const (
	// ContractAdapterCapability — имя публикуемого контракта. Реэкспорт
	// adaptercapability.ContractName.
	ContractAdapterCapability = adaptercapability.ContractName

	// AdapterCapabilitySchemaVersion — текущая версия схемы контракта.
	AdapterCapabilitySchemaVersion = adaptercapability.SchemaVersion
)

// Реэкспорт типов adapter-capability.
type (
	// AdapterCapability — объявление одной возможности адаптера.
	AdapterCapability = adaptercapability.Capability
	// CapabilityDeclaration — публикуемое объявление набора возможностей.
	CapabilityDeclaration = adaptercapability.Declaration
)

// NewCapabilityDeclaration возвращает объявление с проставленными именем и
// версией схемы контракта adapter-capability.
func NewCapabilityDeclaration(env adaptercapability.Envelope) CapabilityDeclaration {
	return adaptercapability.NewDeclaration(env)
}

// ParseCapabilityName сверяет имя возможности контракта с доменным VO: значение,
// недопустимое для домена, отвергается.
func ParseCapabilityName(capability adaptercapability.Capability) (domain.CapabilityName, error) {
	parsed, err := domain.ParseCapabilityName(capability.Name)
	if err != nil {
		return "", fmt.Errorf("contracts: adapter-capability: capability.name: %w", err)
	}
	return parsed, nil
}

// ValidateDeclaration проверяет форму объявления adapter-capability и сверяет
// каждое имя возможности с доменным VO.
func ValidateDeclaration(declaration adaptercapability.Declaration) error {
	if err := declaration.Validate(); err != nil {
		return err
	}
	for i, capability := range declaration.Capabilities {
		if _, err := ParseCapabilityName(capability); err != nil {
			return fmt.Errorf("contracts: adapter-capability: capabilities[%d]: %w", i, err)
		}
	}
	return nil
}
