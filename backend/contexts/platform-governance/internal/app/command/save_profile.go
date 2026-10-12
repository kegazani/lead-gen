package command

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// SaveProfile — клиентская команда сохранения черновика профиля компании.
// Правка запрещена, пока профиль на согласовании (инвариант домена).
type SaveProfile struct {
	TenantID    string
	Description string
	Target      string
	Services    []domain.Service
}

// SaveProfileHandler — use case сохранения профиля компании.
type SaveProfileHandler struct {
	uow ports.UnitOfWork
}

// NewSaveProfileHandler собирает use case из транзакции.
func NewSaveProfileHandler(uow ports.UnitOfWork) *SaveProfileHandler {
	return &SaveProfileHandler{uow: uow}
}

// Handle загружает тенант и сохраняет черновик профиля. Профиль не меняет
// правила доступа и не порождает событий, поэтому outbox не заполняется.
func (h *SaveProfileHandler) Handle(ctx context.Context, cmd SaveProfile) error {
	tenantID := domain.TenantID(strings.TrimSpace(cmd.TenantID))
	if err := tenantID.Validate(); err != nil {
		return err
	}
	profile := domain.Profile{Description: cmd.Description, Target: cmd.Target, Services: cmd.Services}
	return h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		tenant, err := tx.Tenants().FindByID(ctx, tenantID)
		if err != nil {
			return err
		}
		if err := tenant.SaveProfile(profile); err != nil {
			return err
		}
		return tx.Tenants().Save(ctx, tenant)
	})
}
