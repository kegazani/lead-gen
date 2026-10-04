package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// AssignRole — команда назначения роли пользователю тенанта.
type AssignRole struct {
	Actor     string
	CommandID string
	TenantID  string
	UserID    string
	Role      string
}

// AssignRoleHandler — use case назначения роли.
type AssignRoleHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
}

// NewAssignRoleHandler собирает use case из транзакции, часов, генератора
// идентификаторов и платформенных прав.
func NewAssignRoleHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants) *AssignRoleHandler {
	return &AssignRoleHandler{uow: uow, clock: clock, ids: ids, grants: grants}
}

// Handle проверяет право актора, назначает роль пользователю тенанта в одной
// транзакции и публикует новую версию снимка правил. Повтор команды с тем же
// ключом идемпотентен.
func (h *AssignRoleHandler) Handle(ctx context.Context, cmd AssignRole) error {
	actor, userID, role, err := h.parse(ctx, cmd)
	if err != nil {
		return err
	}
	tenantID, commandID, err := commandIDs(cmd.TenantID, cmd.CommandID)
	if err != nil {
		return err
	}
	at := h.clock.Now()
	return h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return changeTenant(ctx, tx, tenantID, commandID, at, func(tenant *domain.Tenant) error {
			return tenant.AssignRole(changeContext(at, actor, h.ids), userID, role)
		})
	})
}

// parse проверяет право актора и разбирает идентификатор пользователя и роль.
func (h *AssignRoleHandler) parse(ctx context.Context, cmd AssignRole) (domain.ActorID, domain.UserID, domain.Role, error) {
	actor := domain.ActorID(strings.TrimSpace(cmd.Actor))
	if err := actor.Validate(); err != nil {
		return "", "", "", fmt.Errorf("%w: %v", app.ErrMissingActor, err)
	}
	if err := app.Authorize(ctx, h.grants, actor, domain.RightManageRoles); err != nil {
		return "", "", "", err
	}
	userID := domain.UserID(strings.TrimSpace(cmd.UserID))
	if userID == "" {
		return "", "", "", domain.ErrEmptyID
	}
	role := domain.Role(strings.TrimSpace(cmd.Role))
	if role == "" {
		return "", "", "", domain.ErrUnknownRole
	}
	return actor, userID, role, nil
}
