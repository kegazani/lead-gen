package command

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
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
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewRegisterClientHandler собирает use case из транзакции, часов и генератора
// идентификаторов.
func NewRegisterClientHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *RegisterClientHandler {
	return &RegisterClientHandler{uow: uow, clock: clock, ids: ids}
}

// Handle создаёт тенант с пользователем роли client, сохраняет его вместе с
// событиями (аудит и изменение доступа) в одной транзакции и возвращает
// идентификаторы. Неполная форма — доменная ошибка, а не пустой тенант.
//
// Регистрация — самообслуживание: актор не требует платформенного гранта, а
// запись аудита фиксирует субъектом самого регистрирующегося клиента.
func (h *RegisterClientHandler) Handle(ctx context.Context, cmd RegisterClient) (RegisterClientResult, error) {
	userID := h.ids.NewUserID()
	roles, err := domain.NewRoleSet(domain.RoleClient)
	if err != nil {
		return RegisterClientResult{}, err
	}
	at := h.clock.Now()
	result := RegisterClientResult{UserID: userID.String(), Login: loginFor(userID)}

	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		tenant, err := domain.RegisterTenant(domain.RegisterTenantParams{
			ID:          h.ids.NewTenantID(),
			CompanyName: cmd.CompanyName,
			Description: cmd.Description,
			At:          at,
			Actor:       domain.ActorID(userID),
			EventID:     h.ids.NewEventID(),
			AuditID:     h.ids.NewAuditRecordID(),
			User:        &domain.NewUser{ID: userID, Login: loginFor(userID), Roles: roles},
		})
		if err != nil {
			return err
		}
		if err := tx.Tenants().Save(ctx, tenant); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, app.ChangeMessages(tenant, tenant.PullDomainEvents(), at)...); err != nil {
			return err
		}
		result.TenantID = tenant.ID().String()
		return nil
	})
	if err != nil {
		return RegisterClientResult{}, err
	}
	return result, nil
}

// loginFor выводит логин клиента из идентификатора пользователя. Логин
// генерируется платформой: отдельного шага выбора логина в самообслуживании нет.
func loginFor(id domain.UserID) string {
	return "client-" + strings.TrimPrefix(id.String(), "user-")
}
