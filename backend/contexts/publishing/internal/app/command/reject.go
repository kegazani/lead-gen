package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Reject — команда отклонения материала оператором. Причина обязательна;
// отклонённый материал не публикуется и остаётся неисполненным.
type Reject struct {
	PublicationID domain.PublicationID
	TenantID      domain.TenantID
	Actor         domain.ActorID
	At            time.Time
	Reason        string
}

// RejectHandler — use case отклонения материала.
type RejectHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
}

// NewRejectHandler собирает use case из порта транзакции и часов.
func NewRejectHandler(uow ports.UnitOfWork, clock ports.Clock) *RejectHandler {
	return &RejectHandler{uow: uow, clock: clock}
}

// Handle отклоняет материал и сохраняет решение в одной транзакции.
func (h *RejectHandler) Handle(ctx context.Context, cmd Reject) (PublicationState, error) {
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
		if err := publication.Reject(cmd.Actor, at, cmd.Reason); err != nil {
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
