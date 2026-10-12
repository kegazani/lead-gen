package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// defaultCallPurpose — код цели звонка по умолчанию: уточнение оффера.
const defaultCallPurpose = "offer-clarification"

// InitiateCall — команда инициирования звонка-уточнения клиенту.
type InitiateCall struct {
	RequestID     domain.RequestID
	TenantID      domain.TenantID
	CorrelationID domain.CorrelationID
	Purpose       string
}

// InitiateCallResult — результат инициирования звонка.
type InitiateCallResult struct {
	RequestID domain.RequestID
	CallID    domain.CallID
	Status    domain.RequestStatus
	Version   int
}

// InitiateCallHandler — use case инициирования звонка-уточнения.
//
// Порядок: сначала проверяется, что звонок допустим (заявка существует, тенант
// совпадает, статус не терминальный), затем вызывается контракт владельца звонка,
// и только после успеха идентификатор звонка фиксируется в заявке. Внешний вызов
// не идёт внутри транзакции записи: сбой контракта не оставляет заявку изменённой.
type InitiateCallHandler struct {
	uow   ports.UnitOfWork
	calls ports.CallInitiator
}

// NewInitiateCallHandler собирает use case из транзакции и порта звонков.
func NewInitiateCallHandler(uow ports.UnitOfWork, calls ports.CallInitiator) *InitiateCallHandler {
	return &InitiateCallHandler{uow: uow, calls: calls}
}

// Handle инициирует звонок и фиксирует его идентификатор в заявке.
func (h *InitiateCallHandler) Handle(ctx context.Context, cmd InitiateCall) (InitiateCallResult, error) {
	if h.calls == nil {
		return InitiateCallResult{}, ports.ErrCallUnavailable
	}
	if err := h.check(ctx, cmd); err != nil {
		return InitiateCallResult{}, err
	}

	purpose := cmd.Purpose
	if purpose == "" {
		purpose = defaultCallPurpose
	}
	result, err := h.calls.InitiateCall(ctx, ports.InitiateCall{
		RequestID:     cmd.RequestID.String(),
		TenantID:      cmd.TenantID.String(),
		CorrelationID: cmd.CorrelationID.String(),
		Purpose:       purpose,
	})
	if err != nil {
		return InitiateCallResult{}, err
	}

	transition, err := applyChange(ctx, h.uow, commandKey{RequestID: cmd.RequestID, TenantID: cmd.TenantID},
		nil,
		func(r *domain.Request) ([]domain.DomainEvent, error) {
			return nil, r.AttachCall(domain.CallID(result.CallID))
		})
	if err != nil {
		return InitiateCallResult{}, err
	}
	return InitiateCallResult{
		RequestID: transition.RequestID,
		CallID:    domain.CallID(result.CallID),
		Status:    transition.Status,
		Version:   transition.Version,
	}, nil
}

// check подтверждает, что звонок допустим: заявка есть, тенант совпадает, статус
// активен. Изменений не вносит.
func (h *InitiateCallHandler) check(ctx context.Context, cmd InitiateCall) error {
	return h.uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
		request, err := tx.Requests().FindByID(txCtx, cmd.RequestID)
		if err != nil {
			return err
		}
		if request.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: заявка %s", ErrTenantMismatch, cmd.RequestID)
		}
		if request.Status().IsTerminal() {
			return fmt.Errorf("%w: заявка %s в статусе %s", domain.ErrTerminalState, cmd.RequestID, request.Status())
		}
		return nil
	})
}
