package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// SubmitRequest — команда подачи новой заявки клиентом.
type SubmitRequest struct {
	RequestID domain.RequestID
	OfferID   domain.OfferID
	TenantID  domain.TenantID
	Content   string
	Target    string
	Actor     domain.ActorID
	At        time.Time
	// CorrelationID — сквозной сценарий; для новой заявки им служит её
	// идентификатор, если значение не задано вызывающим.
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// SubmitRequestResult — результат подачи заявки.
type SubmitRequestResult struct {
	RequestID domain.RequestID
	Status    domain.RequestStatus
	Version   int
	Events    []domain.DomainEvent
}

// SubmitRequestHandler — use case подачи заявки.
type SubmitRequestHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewSubmitRequestHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов событий.
func NewSubmitRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *SubmitRequestHandler {
	return &SubmitRequestHandler{uow: uow, clock: clock, ids: ids}
}

// Handle создаёт заявку и сохраняет её вместе с событием request-submitted в
// одной транзакции. Заявка без непустого оффера не создаётся.
func (h *SubmitRequestHandler) Handle(ctx context.Context, cmd SubmitRequest) (SubmitRequestResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	correlation := cmd.CorrelationID
	if correlation == "" {
		correlation = domain.CorrelationID(cmd.RequestID)
	}

	var result SubmitRequestResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		request, err := domain.NewRequest(domain.NewRequestParams{
			ID:            cmd.RequestID,
			OfferID:       cmd.OfferID,
			TenantID:      cmd.TenantID,
			Content:       cmd.Content,
			Target:        cmd.Target,
			Actor:         cmd.Actor,
			At:            at,
			EventID:       h.ids.NewEventID(),
			CorrelationID: correlation,
			CausationID:   cmd.CausationID,
		})
		if err != nil {
			return err
		}
		if err := tx.Requests().Save(ctx, request, 0); err != nil {
			return err
		}
		events := request.DomainEvents()
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		request.PullDomainEvents()
		result = SubmitRequestResult{
			RequestID: request.ID(),
			Status:    request.Status(),
			Version:   request.Version(),
			Events:    events,
		}
		return nil
	})
	if err != nil {
		return SubmitRequestResult{}, err
	}
	return result, nil
}
