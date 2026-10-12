package command

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// SubmitProfile — клиентская команда отправки заполненного профиля компании на
// согласование администратором.
type SubmitProfile struct {
	TenantID string
}

// SubmitProfileHandler — use case отправки профиля на согласование.
type SubmitProfileHandler struct {
	uow ports.UnitOfWork
}

// NewSubmitProfileHandler собирает use case из транзакции.
func NewSubmitProfileHandler(uow ports.UnitOfWork) *SubmitProfileHandler {
	return &SubmitProfileHandler{uow: uow}
}

// Handle загружает тенант и отправляет профиль на согласование. Профиль не
// меняет правила доступа и не порождает событий, поэтому outbox не заполняется.
func (h *SubmitProfileHandler) Handle(ctx context.Context, cmd SubmitProfile) error {
	tenantID := domain.TenantID(strings.TrimSpace(cmd.TenantID))
	if err := tenantID.Validate(); err != nil {
		return err
	}
	return h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		tenant, err := tx.Tenants().FindByID(ctx, tenantID)
		if err != nil {
			return err
		}
		if err := tenant.SubmitProfile(); err != nil {
			return err
		}
		return tx.Tenants().Save(ctx, tenant)
	})
}
