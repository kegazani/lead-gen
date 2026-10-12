package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// ApproveProfile — команда подтверждения профиля компании уполномоченным
// актором: pending → active.
type ApproveProfile struct {
	TenantID  string
	Actor     string
	CommandID string
}

// ApproveProfileHandler — use case подтверждения профиля компании.
type ApproveProfileHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
}

// NewApproveProfileHandler собирает use case из транзакции, часов, генератора
// идентификаторов и платформенных прав.
func NewApproveProfileHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants) *ApproveProfileHandler {
	return &ApproveProfileHandler{uow: uow, clock: clock, ids: ids, grants: grants}
}

// Handle проверяет право актора, подтверждает профиль и фиксирует запись аудита
// в одной транзакции. Повтор команды с тем же ключом идемпотентен.
func (h *ApproveProfileHandler) Handle(ctx context.Context, cmd ApproveProfile) error {
	actor := domain.ActorID(strings.TrimSpace(cmd.Actor))
	if err := actor.Validate(); err != nil {
		return fmt.Errorf("%w: %v", app.ErrMissingActor, err)
	}
	if err := app.Authorize(ctx, h.grants, actor, domain.RightManageTenants); err != nil {
		return err
	}
	tenantID, commandID, err := commandIDs(cmd.TenantID, cmd.CommandID)
	if err != nil {
		return err
	}
	at := h.clock.Now()
	return h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return changeTenant(ctx, tx, tenantID, commandID, at, func(tenant *domain.Tenant) error {
			return tenant.ApproveProfile(changeContext(at, actor, h.ids))
		})
	})
}
