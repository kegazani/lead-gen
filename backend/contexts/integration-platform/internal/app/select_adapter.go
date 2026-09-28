package app

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// SelectAdapterCommand — команда выбора адаптера по обязательным возможностям.
type SelectAdapterCommand struct {
	// Contract — имя контракта, для которого выбирается адаптер (диагностика).
	Contract string
	// Required — обязательные возможности контракта. Непусты.
	Required []domain.CapabilityName
	// Preferred — заранее названный адаптер. Необязателен.
	Preferred *domain.AdapterName
}

// SelectAdapterHandler — use case выбора адаптера.
type SelectAdapterHandler struct {
	registrations ports.AdapterRegistrationRepository
}

// NewSelectAdapterHandler собирает use case из репозитория регистраций.
func NewSelectAdapterHandler(registrations ports.AdapterRegistrationRepository) *SelectAdapterHandler {
	return &SelectAdapterHandler{registrations: registrations}
}

// Handle загружает регистрации и применяет политику adapter-selection. Доменных
// решений сценарий не содержит: выбор полностью определяет домен.
func (h *SelectAdapterHandler) Handle(ctx context.Context, cmd SelectAdapterCommand) (domain.AdapterSelection, error) {
	registrations, err := h.registrations.List(ctx)
	if err != nil {
		return domain.AdapterSelection{}, fmt.Errorf("загрузка регистраций адаптеров: %w", err)
	}
	return domain.SelectAdapter(registrations, domain.AdapterSelectionRequest{
		Contract:  cmd.Contract,
		Required:  cmd.Required,
		Preferred: cmd.Preferred,
	})
}
