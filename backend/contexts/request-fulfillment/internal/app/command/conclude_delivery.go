package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
)

// ConcludeDelivery — команда исхода доставки письма: сопоставить событие доставки
// с заявкой по кампании и подать бегущему исполнению сигнал завершения
// (capability request-delivery-conclusion). Поля — только идентификаторы.
type ConcludeDelivery struct {
	// TenantID — тенант-владелец события доставки.
	TenantID string
	// CampaignID — кампания, по которой событие сопоставляется с заявкой.
	CampaignID string
	// MessageID — идентификатор сообщения (ключ дедупликации в графе исполнения).
	MessageID string
	// Delivered — true, если письмо доставлено; false — исход недоставки.
	Delivered bool
}

// ConcludeDeliveryResult — наблюдаемый исход обработки события: сопоставлена ли
// заявка и подан ли сигнал.
type ConcludeDeliveryResult struct {
	// Matched — событие сопоставлено с заявкой этого контекста.
	Matched bool
	// RequestID — заявка, к которой отнесено событие (пусто, если не сопоставлено).
	RequestID string
	// Signaled — бегущему исполнению подан сигнал исхода.
	Signaled bool
}

// ConcludeDeliveryHandler — use case обработки события доставки. Он не владеет
// решением о завершении: решение принимает граф исполнения заявки, а обработчик
// лишь маршрутизирует событие к нужному исполнению. Событие чужой кампании или
// чужого тенанта игнорируется: произвольная привязка исключена.
type ConcludeDeliveryHandler struct {
	requests  ports.RequestRepository
	workflows ports.WorkflowSignaler
	logger    observ.Logger
}

// NewConcludeDeliveryHandler собирает use case.
func NewConcludeDeliveryHandler(requests ports.RequestRepository, workflows ports.WorkflowSignaler, logger observ.Logger) *ConcludeDeliveryHandler {
	return &ConcludeDeliveryHandler{requests: requests, workflows: workflows, logger: logger}
}

// Handle сопоставляет событие с заявкой и подаёт сигнал исхода. Заявка не
// найдена — событие игнорируется (не наша кампания). Недоступность исполнения
// (рантайм не подключён, сбой транспорта) возвращается честной ошибкой и не
// выдаётся за доставку. Сигнал завершённому исполнению — не ошибка: заявка уже
// терминальна, повторный эффект не нужен.
func (h *ConcludeDeliveryHandler) Handle(ctx context.Context, cmd ConcludeDelivery) (ConcludeDeliveryResult, error) {
	campaign := domain.CampaignID(strings.TrimSpace(cmd.CampaignID))
	if campaign == "" {
		// Событие без ссылки на кампанию сопоставить не с чем: молча пропускаем,
		// не привязывая к произвольной заявке.
		return ConcludeDeliveryResult{}, nil
	}
	request, err := h.requests.FindByCampaign(ctx, domain.TenantID(strings.TrimSpace(cmd.TenantID)), campaign)
	if errors.Is(err, ports.ErrRequestNotFound) {
		if h.logger != nil {
			h.logger.Info("событие доставки чужой кампании — пропущено", observ.Fields{},
				"campaign_id", cmd.CampaignID, "tenant_id", cmd.TenantID)
		}
		return ConcludeDeliveryResult{}, nil
	}
	if err != nil {
		return ConcludeDeliveryResult{}, fmt.Errorf("сопоставление события доставки с заявкой по кампании %s: %w", campaign, err)
	}
	result := ConcludeDeliveryResult{Matched: true, RequestID: request.ID().String()}

	if h.workflows == nil {
		// Рантайм не подключён — честная недоступность, а не выдуманное завершение.
		return result, ports.ErrWorkflowUnavailable
	}
	err = h.workflows.SignalDeliveryOutcome(ctx, ports.DeliveryOutcome{
		RequestID: request.ID().String(),
		MessageID: cmd.MessageID,
		Delivered: cmd.Delivered,
	})
	switch {
	case err == nil:
		result.Signaled = true
		return result, nil
	case errors.Is(err, ports.ErrWorkflowNotFound):
		// Исполнение уже завершено (например, заявка терминальна): повторный эффект
		// не нужен, ошибкой это не является.
		return result, nil
	default:
		return result, fmt.Errorf("сигнал исхода доставки заявке %s: %w", request.ID(), err)
	}
}
