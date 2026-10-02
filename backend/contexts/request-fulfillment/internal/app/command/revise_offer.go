package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ReviseOffer — команда правки оффера заявки. Правка не меняет существующую
// версию на месте: создаётся новая версия оффера, а прежняя остаётся неизменной.
type ReviseOffer struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
	OfferID   domain.OfferID
	Content   string
	Target    string
	Actor     domain.ActorID
	At        time.Time
}

// ReviseOfferResult — результат правки оффера.
type ReviseOfferResult struct {
	RequestID     domain.RequestID
	Status        domain.RequestStatus
	Version       int
	OfferRevision int
}

// ReviseOfferHandler — use case правки оффера.
type ReviseOfferHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewReviseOfferHandler собирает use case.
func NewReviseOfferHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *ReviseOfferHandler {
	return &ReviseOfferHandler{uow: uow, clock: clock, ids: ids}
}

// Handle создаёт новую версию оффера. Правка запрещена в терминальных статусах.
func (h *ReviseOfferHandler) Handle(ctx context.Context, cmd ReviseOffer) (ReviseOfferResult, error) {
	in := transitionInput(cmd.Actor, cmd.At, "", h.clock, h.ids, "", "")
	var revision int
	result, err := applyChange(ctx, h.uow, commandKey{RequestID: cmd.RequestID, TenantID: cmd.TenantID},
		func(request *domain.Request) ([]domain.DomainEvent, error) {
			offer, err := request.ReviseOffer(in, cmd.OfferID, cmd.Content, cmd.Target)
			if err != nil {
				return nil, err
			}
			revision = offer.Revision()
			return nil, nil
		})
	if err != nil {
		return ReviseOfferResult{}, err
	}
	return ReviseOfferResult{
		RequestID:     result.RequestID,
		Status:        result.Status,
		Version:       result.Version,
		OfferRevision: revision,
	}, nil
}
