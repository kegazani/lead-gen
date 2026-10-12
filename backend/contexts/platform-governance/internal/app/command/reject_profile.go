package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// RejectProfile — команда отклонения профиля компании уполномоченным актором:
// pending → rejected, профиль снова доступен для правки.
type RejectProfile struct {
	TenantID  string
	Actor     string
	CommandID string
}

// RejectProfileHandler — use case отклонения профиля компании.
type RejectProfileHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
}

// NewRejectProfileHandler собирает use case из транзакции, часов, генератора
// идентификаторов и платформенных прав.
func NewRejectProfileHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants) *RejectProfileHandler {
	return &RejectProfileHandler{uow: uow, clock: clock, ids: ids, grants: grants}
}

// Handle проверяет право актора, отклоняет профиль и фиксирует запись аудита в
// одной транзакции. Повтор команды с тем же ключом идемпотентен.
func (h *RejectProfileHandler) Handle(ctx context.Context, cmd RejectProfile) error {
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
			return tenant.RejectProfile(changeContext(at, actor, h.ids))
		})
	})
}
