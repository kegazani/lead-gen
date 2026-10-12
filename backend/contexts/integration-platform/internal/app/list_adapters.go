package app

import (
	"context"
	"fmt"
	"sort"

	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// AdapterRegistrationView — нормализованное представление зарегистрированного
// адаптера для чтения реестра. Поля — строки без доменных типов: имя адаптера,
// версия manifest и объявленные возможности. Секретов здесь нет: manifest их не
// содержит.
type AdapterRegistrationView struct {
	// Adapter — стабильное имя адаптера.
	Adapter string
	// Version — версия manifest.
	Version string
	// Capabilities — объявленные возможности в порядке manifest.
	Capabilities []string
}

// ListAdaptersHandler — use case чтения реестра зарегистрированных адаптеров.
// Отдаёт нейтральные представления, отсортированные по идентификатору
// регистрации, поэтому ответ детерминирован.
type ListAdaptersHandler struct {
	registrations ports.AdapterRegistrationRepository
}

// NewListAdaptersHandler собирает use case из репозитория регистраций.
func NewListAdaptersHandler(registrations ports.AdapterRegistrationRepository) *ListAdaptersHandler {
	return &ListAdaptersHandler{registrations: registrations}
}

// Handle читает реестр адаптеров. Пустой реестр — пустой список без ошибки:
// «адаптеров нет» — реальное состояние, а не недоступность.
func (h *ListAdaptersHandler) Handle(ctx context.Context) ([]AdapterRegistrationView, error) {
	if h == nil || h.registrations == nil {
		return nil, fmt.Errorf("реестр адаптеров не собран")
	}
	registrations, err := h.registrations.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("чтение реестра адаптеров: %w", err)
	}
	views := make([]AdapterRegistrationView, 0, len(registrations))
	for _, registration := range registrations {
		manifest := registration.Manifest()
		capabilities := make([]string, 0, len(manifest.Capabilities()))
		for _, capability := range manifest.Capabilities() {
			capabilities = append(capabilities, string(capability))
		}
		views = append(views, AdapterRegistrationView{
			Adapter:      manifest.AdapterName().String(),
			Version:      manifest.Version().String(),
			Capabilities: capabilities,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Adapter < views[j].Adapter })
	return views, nil
}
