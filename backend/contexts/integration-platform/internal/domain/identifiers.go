package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы и value objects домена integration-platform.
//
// Каждый идентификатор — отдельный именованный тип, чтобы идентификатор регистрации
// нельзя было передать вместо идентификатора вызова, а тип интеграции — вместо
// тенанта. Значения неизменяемы.
type (
	// AdapterID — нейтральный внутренний идентификатор регистрации адаптера.
	AdapterID string

	// InvocationID — нейтральный внутренний идентификатор вызова интеграции.
	InvocationID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// CommandKey — ключ команды, заданный инициатором вызова.
	CommandKey string

	// CorrelationRef — value object correlation-ref: идентификатор, связывающий
	// вызов со сквозным сценарием потребителя. Заполняется из correlation_id
	// конверта входящей команды.
	CorrelationRef string

	// IntegrationType — тип интеграции: группа операций одного поставщика.
	IntegrationType string

	// Operation — имя операции (возможности), исполняемой адаптером.
	Operation string

	// ExternalID — внешний идентификатор, присвоенный поставщиком.
	ExternalID string
)

// String возвращает строковое представление идентификаторов.
func (id AdapterID) String() string       { return string(id) }
func (id InvocationID) String() string    { return string(id) }
func (id TenantID) String() string        { return string(id) }
func (id CommandKey) String() string      { return string(id) }
func (ref CorrelationRef) String() string { return string(ref) }
func (t IntegrationType) String() string  { return string(t) }
func (op Operation) String() string       { return string(op) }
func (e ExternalID) String() string       { return string(e) }

func (id AdapterID) validate() error    { return validateID("adapter_id", string(id)) }
func (id InvocationID) validate() error { return validateID("invocation_id", string(id)) }
func (id TenantID) validate() error     { return validateID("tenant_id", string(id)) }
func (id CommandKey) validate() error   { return validateID("command_key", string(id)) }

// ParseAdapterID разбирает идентификатор регистрации адаптера.
func ParseAdapterID(id string) (AdapterID, error) {
	if err := validateID("adapter_id", id); err != nil {
		return "", err
	}
	return AdapterID(id), nil
}

// ParseInvocationID разбирает идентификатор вызова интеграции.
func ParseInvocationID(id string) (InvocationID, error) {
	if err := validateID("invocation_id", id); err != nil {
		return "", err
	}
	return InvocationID(id), nil
}

// ParseTenantID разбирает идентификатор тенанта.
func ParseTenantID(id string) (TenantID, error) {
	if err := validateID("tenant_id", id); err != nil {
		return "", err
	}
	return TenantID(id), nil
}

// ParseCommandKey разбирает ключ команды инициатора.
func ParseCommandKey(key string) (CommandKey, error) {
	if err := validateID("command_key", key); err != nil {
		return "", err
	}
	return CommandKey(key), nil
}

// ParseCorrelationRef разбирает ссылку на сквозной сценарий.
func ParseCorrelationRef(ref string) (CorrelationRef, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", fmt.Errorf("%w: correlation_id", ErrEmptyCorrelationRef)
	}
	return CorrelationRef(trimmed), nil
}

// ParseIntegrationType разбирает тип интеграции.
func ParseIntegrationType(integrationType string) (IntegrationType, error) {
	trimmed := strings.TrimSpace(integrationType)
	if trimmed == "" {
		return "", fmt.Errorf("%w: integration_type", ErrEmptyIntegrationType)
	}
	return IntegrationType(trimmed), nil
}

// ParseOperation разбирает имя операции.
func ParseOperation(operation string) (Operation, error) {
	trimmed := strings.TrimSpace(operation)
	if trimmed == "" {
		return "", fmt.Errorf("%w: operation", ErrEmptyOperation)
	}
	return Operation(trimmed), nil
}

// ParseExternalID разбирает внешний идентификатор поставщика.
func ParseExternalID(externalID string) (ExternalID, error) {
	trimmed := strings.TrimSpace(externalID)
	if trimmed == "" {
		return "", fmt.Errorf("%w: external_id", ErrEmptyID)
	}
	return ExternalID(trimmed), nil
}

func (ref CorrelationRef) validate() error {
	_, err := ParseCorrelationRef(string(ref))
	return err
}

func (t IntegrationType) validate() error {
	_, err := ParseIntegrationType(string(t))
	return err
}

func (op Operation) validate() error {
	_, err := ParseOperation(string(op))
	return err
}

func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
