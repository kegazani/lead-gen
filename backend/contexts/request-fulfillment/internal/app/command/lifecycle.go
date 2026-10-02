package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// LifecycleFields — общая часть команд переходов статуса над существующей
// заявкой.
type LifecycleFields struct {
	RequestID     domain.RequestID
	TenantID      domain.TenantID
	Actor         domain.ActorID
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// StartValidation — команда начала валидации заявки.
type StartValidation struct{ LifecycleFields }

// ValidateRequest — команда подтверждения валидации заявки.
type ValidateRequest struct{ LifecycleFields }

// StartProcessing — команда запуска исполнения заявки.
type StartProcessing struct{ LifecycleFields }

// FulfillRequest — команда завершения исполнения заявки.
type FulfillRequest struct{ LifecycleFields }

// RejectRequest — команда отклонения заявки по итогам валидации.
type RejectRequest struct {
	LifecycleFields
	Reason string
}

// CancelRequest — команда отмены заявки.
type CancelRequest struct {
	LifecycleFields
	Reason string
}

// handler — общий каркас сценарного обработчика перехода.
type handler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// advance выполняет переход, порождаемый методом transition.
func (h handler) advance(
	ctx context.Context,
	fields LifecycleFields,
	reason string,
	transition func(request *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error),
) (TransitionResult, error) {
	in := transitionInput(
		fields.Actor, fields.At, reason, h.clock, h.ids,
		fields.CorrelationID, fields.CausationID,
	)
	return applyChange(ctx, h.uow, commandKey{RequestID: fields.RequestID, TenantID: fields.TenantID},
		func(request *domain.Request) ([]domain.DomainEvent, error) {
			return transition(request, in)
		})
}

// StartValidationHandler — use case начала валидации.
type StartValidationHandler struct{ handler }

// NewStartValidationHandler собирает use case.
func NewStartValidationHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *StartValidationHandler {
	return &StartValidationHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку submitted → validating.
func (h *StartValidationHandler) Handle(ctx context.Context, cmd StartValidation) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, "", func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.StartValidation(in)
	})
}

// ValidateRequestHandler — use case подтверждения валидации.
type ValidateRequestHandler struct{ handler }

// NewValidateRequestHandler собирает use case.
func NewValidateRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *ValidateRequestHandler {
	return &ValidateRequestHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку validating → validated.
func (h *ValidateRequestHandler) Handle(ctx context.Context, cmd ValidateRequest) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, "", func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.Validate(in)
	})
}

// StartProcessingHandler — use case запуска исполнения.
type StartProcessingHandler struct{ handler }

// NewStartProcessingHandler собирает use case.
func NewStartProcessingHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *StartProcessingHandler {
	return &StartProcessingHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку validated → processing.
func (h *StartProcessingHandler) Handle(ctx context.Context, cmd StartProcessing) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, "", func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.StartProcessing(in)
	})
}

// FulfillRequestHandler — use case завершения исполнения.
type FulfillRequestHandler struct{ handler }

// NewFulfillRequestHandler собирает use case.
func NewFulfillRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *FulfillRequestHandler {
	return &FulfillRequestHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку processing → fulfilled.
func (h *FulfillRequestHandler) Handle(ctx context.Context, cmd FulfillRequest) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, "", func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.Fulfill(in)
	})
}

// RejectRequestHandler — use case отклонения заявки.
type RejectRequestHandler struct{ handler }

// NewRejectRequestHandler собирает use case.
func NewRejectRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *RejectRequestHandler {
	return &RejectRequestHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку validating → rejected. Причина обязательна.
func (h *RejectRequestHandler) Handle(ctx context.Context, cmd RejectRequest) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, cmd.Reason, func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.Reject(in)
	})
}

// CancelRequestHandler — use case отмены заявки.
type CancelRequestHandler struct{ handler }

// NewCancelRequestHandler собирает use case.
func NewCancelRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *CancelRequestHandler {
	return &CancelRequestHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку из активного статуса в cancelled. Причина обязательна.
func (h *CancelRequestHandler) Handle(ctx context.Context, cmd CancelRequest) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, cmd.Reason, func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.Cancel(in)
	})
}
