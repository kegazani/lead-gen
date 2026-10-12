package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// ResetClientPassword — команда установки нового пароля пользователю клиента
// уполномоченным актором. Сырой пароль в домен не попадает: хешируется здесь.
type ResetClientPassword struct {
	Actor       string
	TenantID    string
	UserID      string
	NewPassword string
}

// ResetClientPasswordHandler — use case сброса пароля клиента.
type ResetClientPasswordHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
	hasher ports.PasswordHasher
}

// NewResetClientPasswordHandler собирает use case из транзакции, часов,
// генератора идентификаторов, платформенных прав и хешера паролей.
func NewResetClientPasswordHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants, hasher ports.PasswordHasher) *ResetClientPasswordHandler {
	return &ResetClientPasswordHandler{uow: uow, clock: clock, ids: ids, grants: grants, hasher: hasher}
}

// Handle проверяет право актора, хеширует новый пароль и заменяет хеш
// пользователя, сохраняя изменение в одной транзакции.
func (h *ResetClientPasswordHandler) Handle(ctx context.Context, cmd ResetClientPassword) error {
	actor := domain.ActorID(strings.TrimSpace(cmd.Actor))
	if err := actor.Validate(); err != nil {
		return fmt.Errorf("%w: %v", app.ErrMissingActor, err)
	}
	if err := app.Authorize(ctx, h.grants, actor, domain.RightManageTenants); err != nil {
		return err
	}
	tenantID := domain.TenantID(strings.TrimSpace(cmd.TenantID))
	if err := tenantID.Validate(); err != nil {
		return err
	}
	userID := domain.UserID(strings.TrimSpace(cmd.UserID))
	if err := userID.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(cmd.NewPassword) == "" {
		return domain.ErrUserPasswordRequired
	}
	hash, err := h.hasher.Hash(cmd.NewPassword)
	if err != nil {
		return err
	}
	at := h.clock.Now()
	return h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		tenant, err := tx.Tenants().FindByID(ctx, tenantID)
		if err != nil {
			return err
		}
		if err := tenant.SetUserPassword(changeContext(at, actor, h.ids), userID, hash); err != nil {
			return err
		}
		if err := tx.Tenants().Save(ctx, tenant); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, app.ChangeMessages(tenant, tenant.PullDomainEvents(), at)...)
	})
}
