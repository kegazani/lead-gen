package command

import (
	"context"
	"fmt"
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

// FailRequest — команда терминального сбоя исполнения заявки (processing →
// failed). ReasonCode — машинный код причины из согласованного набора, Reason —
// текст сообщения оператору. Симметрична CancelRequest, но отражает сбой
// исполнения, а не отмену человеком.
type FailRequest struct {
	LifecycleFields
	ReasonCode string
	Reason     string
}

// RerunRequest — команда явного повторного запуска упавшей заявки
// (failed → validated). Отдельная операция вне обычной таблицы переходов:
// оператор сознательно возвращает заявку на стартовую позицию исполнения.
type RerunRequest struct {
	LifecycleFields
	Reason string
}

// ReturnForRevision — команда возврата заявки клиенту на доработку.
type ReturnForRevision struct {
	LifecycleFields
	Reason string
}

// ResubmitRequest — команда повторной подачи доработанной заявки на проверку.
type ResubmitRequest struct{ LifecycleFields }

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
		nil,
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

// Handle переводит заявку validated → processing. Отклоняет запуск, если у
// тенанта уже есть заявка в работе: не более одной одновременно (лимит).
func (h *StartProcessingHandler) Handle(ctx context.Context, cmd StartProcessing) (TransitionResult, error) {
	in := transitionInput(
		cmd.Actor, cmd.At, "", h.clock, h.ids,
		cmd.CorrelationID, cmd.CausationID,
	)
	return applyChange(ctx, h.uow, commandKey{RequestID: cmd.RequestID, TenantID: cmd.TenantID},
		func(txCtx context.Context, tx ports.Transaction, request *domain.Request) error {
			// Идемпотентный повтор запуска уже работающей заявки — не лимит: он не
			// занимает второй слот. Лимит проверяется только при действительном
			// переходе в processing.
			if request.Status() == domain.StatusProcessing {
				return nil
			}
			inWork, err := tx.Requests().CountInWork(txCtx, cmd.TenantID)
			if err != nil {
				return err
			}
			if inWork >= 1 {
				return fmt.Errorf("%w: у клиента %s уже есть заявка в работе", domain.ErrActiveRequestLimitReached, cmd.TenantID)
			}
			return nil
		},
		func(request *domain.Request) ([]domain.DomainEvent, error) {
			return request.StartProcessing(in)
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

// FailRequestHandler — use case терминального сбоя исполнения.
type FailRequestHandler struct{ handler }

// NewFailRequestHandler собирает use case.
func NewFailRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *FailRequestHandler {
	return &FailRequestHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку processing → failed с кодом и текстом причины. Повтор
// по уже failed заявке — no-op (решает домен); из другого терминального статуса —
// доменная ошибка.
func (h *FailRequestHandler) Handle(ctx context.Context, cmd FailRequest) (TransitionResult, error) {
	in := transitionInput(
		cmd.Actor, cmd.At, cmd.Reason, h.clock, h.ids,
		cmd.CorrelationID, cmd.CausationID,
	)
	in.ReasonCode = cmd.ReasonCode
	return applyChange(ctx, h.uow, commandKey{RequestID: cmd.RequestID, TenantID: cmd.TenantID},
		nil,
		func(request *domain.Request) ([]domain.DomainEvent, error) {
			return request.Fail(in)
		})
}

// RerunRequestHandler — use case явного повторного запуска упавшей заявки.
type RerunRequestHandler struct{ handler }

// NewRerunRequestHandler собирает use case.
func NewRerunRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *RerunRequestHandler {
	return &RerunRequestHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку failed → validated отдельным переходом. Повтор по уже
// validated заявке — no-op (решает домен).
func (h *RerunRequestHandler) Handle(ctx context.Context, cmd RerunRequest) (TransitionResult, error) {
	in := transitionInput(
		cmd.Actor, cmd.At, cmd.Reason, h.clock, h.ids,
		cmd.CorrelationID, cmd.CausationID,
	)
	return applyChange(ctx, h.uow, commandKey{RequestID: cmd.RequestID, TenantID: cmd.TenantID},
		nil,
		func(request *domain.Request) ([]domain.DomainEvent, error) {
			return request.Rerun(in)
		})
}

// ReturnForRevisionHandler — use case возврата заявки на доработку.
type ReturnForRevisionHandler struct{ handler }

// NewReturnForRevisionHandler собирает use case.
func NewReturnForRevisionHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *ReturnForRevisionHandler {
	return &ReturnForRevisionHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку validating | processing → needs-revision. Причина
// обязательна.
func (h *ReturnForRevisionHandler) Handle(ctx context.Context, cmd ReturnForRevision) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, cmd.Reason, func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.ReturnForRevision(in)
	})
}

// ResubmitRequestHandler — use case повторной подачи доработанной заявки.
type ResubmitRequestHandler struct{ handler }

// NewResubmitRequestHandler собирает use case.
func NewResubmitRequestHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *ResubmitRequestHandler {
	return &ResubmitRequestHandler{handler{uow: uow, clock: clock, ids: ids}}
}

// Handle переводит заявку needs-revision → submitted.
func (h *ResubmitRequestHandler) Handle(ctx context.Context, cmd ResubmitRequest) (TransitionResult, error) {
	return h.advance(ctx, cmd.LifecycleFields, "", func(r *domain.Request, in domain.TransitionInput) ([]domain.DomainEvent, error) {
		return r.Resubmit(in)
	})
}
