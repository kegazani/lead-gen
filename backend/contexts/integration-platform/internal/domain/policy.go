package domain

import (
	"fmt"
	"strings"
)

// AdapterSelectionRequest — запрос выбора адаптера.
type AdapterSelectionRequest struct {
	// Contract — имя контракта, для которого выбирается адаптер. Используется
	// только в диагностике (например, provider-port).
	Contract string
	// Required — обязательные возможности контракта. Непусто; каждое имя
	// объявлено доменом.
	Required []CapabilityName
	// Preferred — заранее названный адаптер. Если задан, выбирается именно он, а
	// отсутствие регистрации — диагностика без секретов.
	Preferred *AdapterName
}

// AdapterSelection — результат выбора адаптера.
type AdapterSelection struct {
	// Adapter — выбранная регистрация.
	Adapter *AdapterRegistration
	// Contract — имя контракта, для которого сделан выбор.
	Contract string
	// Required — проверенные обязательные возможности.
	Required []CapabilityName
}

// SelectAdapter — политика adapter-selection: набор регистраций × обязательные
// возможности → выбранный адаптер.
//
// Чистая функция: не имеет побочных эффектов, не обращается к хранилищу и не
// читает время. Неизвестный адаптер или недостающая обязательная возможность —
// диагностика с именем контракта и адаптера, без секретов. Секретами имена
// адаптеров и возможности не являются.
func SelectAdapter(registrations []*AdapterRegistration, request AdapterSelectionRequest) (AdapterSelection, error) {
	if len(request.Required) == 0 {
		return AdapterSelection{}, ErrEmptyRequiredCapabilities
	}
	for _, capability := range request.Required {
		if !IsValidCapabilityName(capability) {
			return AdapterSelection{}, fmt.Errorf("%w: %q", ErrUnknownCapability, capability)
		}
	}
	contract := strings.TrimSpace(request.Contract)

	if request.Preferred != nil {
		name := *request.Preferred
		if err := name.validate(); err != nil {
			return AdapterSelection{}, err
		}
		for _, registration := range registrations {
			if registration == nil || registration.Name() != name {
				continue
			}
			if err := requireAll(registration, request.Required); err != nil {
				return AdapterSelection{}, fmt.Errorf("%s: %w", diagnosticContract(contract), err)
			}
			return newSelection(registration, contract, request.Required), nil
		}
		return AdapterSelection{}, fmt.Errorf(
			"%w: %s: адаптер %q не зарегистрирован",
			ErrAdapterNotFound, diagnosticContract(contract), name,
		)
	}

	for _, registration := range registrations {
		if registration == nil {
			continue
		}
		if requireAll(registration, request.Required) == nil {
			return newSelection(registration, contract, request.Required), nil
		}
	}
	return AdapterSelection{}, fmt.Errorf(
		"%w: %s: ни один адаптер не объявил возможности %s",
		ErrAdapterNotFound, diagnosticContract(contract), formatCapabilities(request.Required),
	)
}

// requireAll проверяет, что регистрация объявляет все требуемые возможности.
func requireAll(registration *AdapterRegistration, required []CapabilityName) error {
	for _, capability := range required {
		if err := registration.Require(capability); err != nil {
			return err
		}
	}
	return nil
}

func newSelection(registration *AdapterRegistration, contract string, required []CapabilityName) AdapterSelection {
	return AdapterSelection{
		Adapter:  registration,
		Contract: contract,
		Required: append([]CapabilityName(nil), required...),
	}
}

// diagnosticContract возвращает безопасное описание контракта для диагностики.
func diagnosticContract(contract string) string {
	if contract == "" {
		return "контракт"
	}
	return fmt.Sprintf("контракт %q", contract)
}

// formatCapabilities перечисляет возможности для диагностики.
func formatCapabilities(capabilities []CapabilityName) string {
	names := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		names = append(names, fmt.Sprintf("%q", capability))
	}
	return strings.Join(names, ", ")
}
