package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// RegisterAdapterCommand — команда регистрации адаптера.
type RegisterAdapterCommand struct {
	// AdapterID — нейтральный идентификатор регистрации. Обязателен.
	AdapterID domain.AdapterID
	// AdapterName — стабильное имя адаптера. Обязательно и уникально.
	AdapterName domain.AdapterName
	// ManifestVersion — версия manifest. Обязательна.
	ManifestVersion domain.ManifestVersion
	// Capabilities — объявляемые возможности. Непусты и объявлены доменом.
	Capabilities []domain.CapabilityName
	// RegisteredAt — момент регистрации. Обязателен.
	RegisteredAt time.Time
}

// RegisterAdapterResult — результат регистрации.
type RegisterAdapterResult struct {
	AdapterID domain.AdapterID
	// Created равно true у успешной регистрации. Повтор имени — ошибка, поэтому
	// значение всегда true при отсутствии ошибки.
	Created bool
	// Events — неподтверждённые события регистрации (adapter-registered).
	Events []domain.DomainEvent
}

// RegisterAdapterHandler — use case регистрации адаптера.
type RegisterAdapterHandler struct {
	registrations ports.AdapterRegistrationRepository
}

// NewRegisterAdapterHandler собирает use case из репозитория регистраций.
func NewRegisterAdapterHandler(registrations ports.AdapterRegistrationRepository) *RegisterAdapterHandler {
	return &RegisterAdapterHandler{registrations: registrations}
}

// Handle создаёт регистрацию через домен и сохраняет её. Повторная регистрация
// того же имени — отказ без изменения существующей.
func (h *RegisterAdapterHandler) Handle(ctx context.Context, cmd RegisterAdapterCommand) (RegisterAdapterResult, error) {
	if _, err := domain.ParseAdapterID(string(cmd.AdapterID)); err != nil {
		return RegisterAdapterResult{}, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	if strings.TrimSpace(string(cmd.AdapterName)) == "" {
		return RegisterAdapterResult{}, fmt.Errorf("%w: adapter_name", ErrInvalidCommand)
	}

	existing, err := h.registrations.FindByName(ctx, cmd.AdapterName)
	switch {
	case err == nil && existing != nil:
		return RegisterAdapterResult{}, fmt.Errorf("%w: %q", ErrAdapterNameTaken, cmd.AdapterName)
	case err != nil && !errors.Is(err, ports.ErrAdapterRegistrationNotFound):
		return RegisterAdapterResult{}, fmt.Errorf("проверка имени адаптера %q: %w", cmd.AdapterName, err)
	}

	manifest, err := domain.NewManifest(cmd.AdapterName, cmd.ManifestVersion, cmd.Capabilities)
	if err != nil {
		return RegisterAdapterResult{}, err
	}
	registration, err := domain.RegisterAdapter(domain.RegisterAdapterParams{
		ID:           cmd.AdapterID,
		Manifest:     manifest,
		RegisteredAt: cmd.RegisteredAt,
	})
	if err != nil {
		return RegisterAdapterResult{}, err
	}
	if err := h.registrations.Save(ctx, registration); err != nil {
		return RegisterAdapterResult{}, fmt.Errorf("сохранение регистрации адаптера %q: %w", cmd.AdapterName, err)
	}
	return RegisterAdapterResult{
		AdapterID: registration.ID(),
		Created:   true,
		Events:    registration.PullDomainEvents(),
	}, nil
}
