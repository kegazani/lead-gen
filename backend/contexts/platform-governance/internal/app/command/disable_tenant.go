package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// DisableTenant — команда отключения тенанта уполномоченным актором.
type DisableTenant struct {
	Actor     string
	CommandID string
	TenantID  string
}

// DisableTenantHandler — use case отключения тенанта.
type DisableTenantHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
}

// NewDisableTenantHandler собирает use case из транзакции, часов, генератора
// идентификаторов и платформенных прав.
func NewDisableTenantHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants) *DisableTenantHandler {
	return &DisableTenantHandler{uow: uow, clock: clock, ids: ids, grants: grants}
}

// Handle проверяет право актора и отключает тенант, фиксируя запись аудита и
// публикуя снимок правил. Отключение идемпотентно: повтор не создаёт новых
// записей.
func (h *DisableTenantHandler) Handle(ctx context.Context, cmd DisableTenant) error {
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
			return tenant.Disable(changeContext(at, actor, h.ids))
		})
	})
}
