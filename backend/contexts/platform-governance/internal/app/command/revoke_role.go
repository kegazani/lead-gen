package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// RevokeRole — команда отзыва роли у пользователя тенанта.
type RevokeRole struct {
	Actor     string
	CommandID string
	TenantID  string
	UserID    string
	Role      string
}

// RevokeRoleHandler — use case отзыва роли.
type RevokeRoleHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
}

// NewRevokeRoleHandler собирает use case из транзакции, часов, генератора
// идентификаторов и платформенных прав.
func NewRevokeRoleHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants) *RevokeRoleHandler {
	return &RevokeRoleHandler{uow: uow, clock: clock, ids: ids, grants: grants}
}

// Handle проверяет право актора, отзывает роль у пользователя тенанта в одной
// транзакции и публикует новую версию снимка правил. Прежний набор ролей не
// меняется: изменение создаёт новый набор.
func (h *RevokeRoleHandler) Handle(ctx context.Context, cmd RevokeRole) error {
	actor := domain.ActorID(strings.TrimSpace(cmd.Actor))
	if err := actor.Validate(); err != nil {
		return fmt.Errorf("%w: %v", app.ErrMissingActor, err)
	}
	if err := app.Authorize(ctx, h.grants, actor, domain.RightManageRoles); err != nil {
		return err
	}
	userID := domain.UserID(strings.TrimSpace(cmd.UserID))
	if userID == "" {
		return domain.ErrEmptyID
	}
	role := domain.Role(strings.TrimSpace(cmd.Role))
	if role == "" {
		return domain.ErrUnknownRole
	}
	tenantID, commandID, err := commandIDs(cmd.TenantID, cmd.CommandID)
	if err != nil {
		return err
	}
	at := h.clock.Now()
	return h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return changeTenant(ctx, tx, tenantID, commandID, at, func(tenant *domain.Tenant) error {
			return tenant.RevokeRole(changeContext(at, actor, h.ids), userID, role)
		})
	})
}
