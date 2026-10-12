package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// ErrRequestCancelled — поздняя команда отвергнута сохранённым tombstone.
var ErrRequestCancelled = errors.New("заявка отменена")

// ErrCancellationPending — owner tombstone сохранён, но delivery owner не
// подтвердил campaign fence; повтор CancelRequest должен повторить propagation.
var ErrCancellationPending = errors.New("отмена ожидает подтверждения владельца доставки")

// CancelRequest — idempotent owner command; ключ определяется tenant + request.
type CancelRequest struct{ TenantID, RequestID string }

// CancelRequestHandler сохраняет отмену даже до создания кампании и затем
// синхронно передаёт campaign fence владельцу доставки.
type CancelRequestHandler struct {
	uow       ports.UnitOfWork
	canceller approvedaction.Canceller
}

func NewCancelRequestHandler(uow ports.UnitOfWork, cancellers ...approvedaction.Canceller) *CancelRequestHandler {
	h := &CancelRequestHandler{uow: uow}
	if len(cancellers) > 0 {
		h.canceller = cancellers[0]
	}
	return h
}

func (h *CancelRequestHandler) Handle(ctx context.Context, cmd CancelRequest) error {
	tenantID, requestID := strings.TrimSpace(cmd.TenantID), strings.TrimSpace(cmd.RequestID)
	if tenantID == "" || requestID == "" {
		return fmt.Errorf("отмена заявки: обязательны tenant_id и request_id")
	}
	if err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		fence, ok := tx.(ports.TransactionRequestCancellationFence)
		if !ok || fence.RequestCancellationFence() == nil {
			return errors.New("отмена заявки: адаптер не поддерживает durable fence")
		}
		return fence.RequestCancellationFence().Cancel(ctx, tenantID, requestID)
	}); err != nil {
		return err
	}

	// Dev/test-сборка может иметь только локальный owner fence. Это не означает,
	// что отмена доставлена; production composition должна передать Canceller.
	if h.canceller == nil {
		return nil
	}
	cancellation := approvedaction.CampaignCancellation{
		TenantID:   tenantID,
		CampaignID: string(handoffCampaignID(requestID)),
	}
	if err := h.canceller.CancelCampaign(ctx, cancellation); err != nil {
		return fmt.Errorf("%w: delivery cancellation unavailable: %v", ErrCancellationPending, err)
	}
	return nil
}

func rejectIfCampaignCancelled(ctx context.Context, tx ports.Transaction, tenantID, campaignID string) error {
	fenced, ok := tx.(ports.TransactionRequestCancellationFence)
	if !ok {
		return errors.New("проверка отмены заявки: адаптер не поддерживает durable fence")
	}
	store := fenced.RequestCancellationFence()
	if store == nil {
		return errors.New("проверка отмены заявки: адаптер не предоставил fence")
	}
	cancelled, err := store.CampaignCancelled(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if cancelled {
		return fmt.Errorf("%w: campaign_id=%s", ErrRequestCancelled, campaignID)
	}
	return nil
}
