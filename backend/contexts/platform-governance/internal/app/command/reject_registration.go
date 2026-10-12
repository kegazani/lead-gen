package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// RejectRegistration — команда отклонения заявки на регистрацию тенанта
// уполномоченным актором: pending → rejected, причина обязательна.
type RejectRegistration struct {
	TenantID    string
	Actor       string
	CommandID   string
	Description string
}

// RejectRegistrationHandler — use case отклонения заявки на регистрацию.
type RejectRegistrationHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
}

// NewRejectRegistrationHandler собирает use case из транзакции, часов,
// генератора идентификаторов и платформенных прав.
func NewRejectRegistrationHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants) *RejectRegistrationHandler {
	return &RejectRegistrationHandler{uow: uow, clock: clock, ids: ids, grants: grants}
}

// Handle проверяет право актора, отклоняет заявку на регистрацию и публикует
// изменение доступа в одной транзакции. Повтор команды с тем же ключом
// идемпотентен.
func (h *RejectRegistrationHandler) Handle(ctx context.Context, cmd RejectRegistration) error {
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
			return tenant.RejectRegistration(changeContext(at, actor, h.ids), cmd.Description)
		})
	})
}
