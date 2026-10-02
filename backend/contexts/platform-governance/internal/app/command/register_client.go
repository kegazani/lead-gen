package command

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// RegisterClient — команда самообслуживания: регистрация тенанта клиента с
// названием и описанием компании.
type RegisterClient struct {
	CompanyName string
	Description string
}

// RegisterClientResult — результат регистрации: идентификаторы созданного тенанта
// и пользователя роли клиента.
type RegisterClientResult struct {
	TenantID string
	UserID   string
	Login    string
}

// RegisterClientHandler — use case регистрации клиента.
type RegisterClientHandler struct {
	tenants ports.TenantRepository
	clock   ports.Clock
	ids     ports.IDGenerator
}

// NewRegisterClientHandler собирает use case из репозитория тенантов, часов и
// генератора идентификаторов.
func NewRegisterClientHandler(tenants ports.TenantRepository, clock ports.Clock, ids ports.IDGenerator) *RegisterClientHandler {
	return &RegisterClientHandler{tenants: tenants, clock: clock, ids: ids}
}

// Handle создаёт тенант с пользователем роли client, сохраняет его и возвращает
// идентификаторы. Неполная форма — доменная ошибка, а не пустой тенант.
func (h *RegisterClientHandler) Handle(ctx context.Context, cmd RegisterClient) (RegisterClientResult, error) {
	userID := h.ids.NewUserID()
	tenant, err := domain.RegisterTenant(domain.RegisterTenantParams{
		ID:          h.ids.NewTenantID(),
		CompanyName: cmd.CompanyName,
		Description: cmd.Description,
		UserID:      userID,
		Login:       loginFor(userID),
		Role:        domain.RoleClient,
		At:          h.clock.Now(),
		EventID:     h.ids.NewEventID(),
	})
	if err != nil {
		return RegisterClientResult{}, err
	}
	if err := h.tenants.Save(ctx, tenant); err != nil {
		return RegisterClientResult{}, err
	}
	return RegisterClientResult{
		TenantID: tenant.ID().String(),
		UserID:   userID.String(),
		Login:    loginFor(userID),
	}, nil
}

// loginFor выводит логин клиента из идентификатора пользователя. Логин
// генерируется платформой: отдельного шага выбора логина в самообслуживании нет.
func loginFor(id domain.UserID) string {
	return "client-" + strings.TrimPrefix(id.String(), "user-")
}
