package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// maxNonTerminalRequests — предельное число одновременных заявок клиента в
// нетерминальных статусах. Создание сверх лимита отклоняется.
const maxNonTerminalRequests = 5

// SubmitRequest — команда подачи новой заявки клиентом.
type SubmitRequest struct {
	RequestID        domain.RequestID
	OfferID          domain.OfferID
	TenantID         domain.TenantID
	Title            string
	Content          string
	Target           string
	ExecutionPurpose string
	SenderCompany    *domain.SenderCompanySnapshot
	Actor            domain.ActorID
	At               time.Time
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
	uow                   ports.UnitOfWork
	clock                 ports.Clock
	ids                   ports.IDGenerator
	executionHistoryKnown bool
}

// NewSubmitRequestHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов событий.
func NewSubmitRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *SubmitRequestHandler {
	return &SubmitRequestHandler{uow: uow, clock: clock, ids: ids}
}

// NewSubmitRequestHandlerWithExecutionHistoryKnown помечает новые заявки только
// при явном протоколе, гарантирующем отсутствие у них legacy execution history.
func NewSubmitRequestHandlerWithExecutionHistoryKnown(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, known bool) *SubmitRequestHandler {
	return &SubmitRequestHandler{uow: uow, clock: clock, ids: ids, executionHistoryKnown: known}
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
	err := h.uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
		params := domain.NewRequestParams{
			ID: cmd.RequestID, OfferID: cmd.OfferID, TenantID: cmd.TenantID,
			Title: cmd.Title, Content: cmd.Content, Target: cmd.Target, ExecutionHistoryKnown: h.executionHistoryKnown,
			ExecutionPurpose: cmd.ExecutionPurpose, SenderCompany: cmd.SenderCompany,
			Actor: cmd.Actor, At: at, CorrelationID: correlation, CausationID: cmd.CausationID,
		}
		if existing, findErr := tx.Requests().FindByID(txCtx, cmd.RequestID); findErr == nil {
			if !existing.SameSubmission(params) {
				return domain.ErrRequestSubmissionConflict
			}
			result = SubmitRequestResult{RequestID: existing.ID(), Status: existing.Status(), Version: existing.Version()}
			return nil
		} else if !errors.Is(findErr, ports.ErrRequestNotFound) {
			return findErr
		}
		count, err := tx.Requests().CountByTenant(txCtx, cmd.TenantID)
		if err != nil {
			return err
		}
		if count >= maxNonTerminalRequests {
			return fmt.Errorf("%w: у клиента %s уже %d заявок в работе", domain.ErrRequestLimitReached, cmd.TenantID, count)
		}
		params.EventID = h.ids.NewEventID()
		request, err := domain.NewRequest(params)
		if err != nil {
			return err
		}
		if err := tx.Requests().Save(txCtx, request, 0); err != nil {
			return err
		}
		events := request.DomainEvents()
		if err := tx.Outbox().Append(txCtx, events...); err != nil {
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
