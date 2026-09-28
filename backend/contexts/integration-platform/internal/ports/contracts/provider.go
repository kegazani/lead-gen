package contracts

import (
	"fmt"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/contracts/integration-platform/providerport"
)

// Реэкспорт опубликованного контракта provider-port: форма запроса и результата,
// их валидация и ключ идемпотентности определены в модуле
// lead-gen/contracts/integration-platform/providerport, а не здесь.

const (
	// ContractProviderPort — имя публикуемого контракта. Реэкспорт
	// providerport.ContractName.
	ContractProviderPort = providerport.ContractName

	// ProviderPortSchemaVersion — текущая версия схемы контракта.
	ProviderPortSchemaVersion = providerport.SchemaVersion
)

// Реэкспорт типов provider-port: определение структуры принадлежит
// опубликованному модулю, а внутренние места вызова используют их через этот
// пакет.
type (
	// ProviderEnvelope — общий конверт контракта provider-port.
	ProviderEnvelope = providerport.Envelope
	// ProviderRequest — DTO синхронного командного вызова.
	ProviderRequest = providerport.Request
	// ProviderResult — DTO результата вызова.
	ProviderResult = providerport.Result
	// ProviderPort — интерфейс контракта provider-port.
	ProviderPort = providerport.Port
	// ContractIntegrationType — тип интеграции в терминах контракта.
	ContractIntegrationType = providerport.IntegrationType
	// ContractOperation — имя операции в терминах контракта.
	ContractOperation = providerport.Operation
)

// NewProviderEnvelope возвращает конверт с проставленными именем и версией схемы
// контракта provider-port.
func NewProviderEnvelope(env providerport.Envelope) providerport.Envelope {
	env.Name = providerport.ContractName
	env.SchemaVersion = providerport.SchemaVersion
	return env
}

// ParseIntegrationType сверяет тип интеграции контракта с доменным VO: значение,
// недопустимое для домена, отвергается.
func ParseIntegrationType(integrationType providerport.IntegrationType) (domain.IntegrationType, error) {
	parsed, err := domain.ParseIntegrationType(string(integrationType))
	if err != nil {
		return "", fmt.Errorf("contracts: provider-port: integration_type: %w", err)
	}
	return parsed, nil
}

// ParseOperation сверяет имя операции контракта с доменным VO.
func ParseOperation(operation providerport.Operation) (domain.Operation, error) {
	parsed, err := domain.ParseOperation(string(operation))
	if err != nil {
		return "", fmt.Errorf("contracts: provider-port: operation: %w", err)
	}
	return parsed, nil
}
