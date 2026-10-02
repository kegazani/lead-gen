package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Approve — команда утверждения материала человеком (policy
// approved-publication-only). Автор и время решения обязательны.
type Approve struct {
	PublicationID domain.PublicationID
	TenantID      domain.TenantID
	Actor         domain.ActorID
	At            time.Time
}

// ApproveHandler — use case утверждения материала.
type ApproveHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
}

// NewApproveHandler собирает use case из порта транзакции и часов.
func NewApproveHandler(uow ports.UnitOfWork, clock ports.Clock) *ApproveHandler {
	return &ApproveHandler{uow: uow, clock: clock}
}

// Handle утверждает материал и сохраняет решение в одной транзакции. Утверждение
// не эмитит доменного события: фактом домена является публикация, а решение
// остаётся в журнале переходов агрегата.
func (h *ApproveHandler) Handle(ctx context.Context, cmd Approve) (PublicationState, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	var state PublicationState
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		publication, err := loadForTenant(ctx, tx, cmd.PublicationID, cmd.TenantID)
		if err != nil {
			return err
		}
		expected := publication.Version()
		if err := publication.Approve(cmd.Actor, at); err != nil {
			return err
		}
		if err := tx.Publications().Save(ctx, publication, expected); err != nil {
			return err
		}
		state = stateOf(publication)
		return nil
	})
	if err != nil {
		return PublicationState{}, err
	}
	return state, nil
}
