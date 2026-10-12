package query

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ApprovalPhaseAwaitingApproval — фаза workflow, на которой исполнение стоит
// на человеческой паузе и ждёт решения оператора.
const ApprovalPhaseAwaitingApproval = "awaiting-approval"

// ErrApprovalRuntimeUnavailable — рантайм чтения состояния исполнения недоступен.
// Это отдельный исход очереди утверждения, а не пустая очередь: экран обязан
// показать «исполнение недоступно», а не выдумать ноль.
var ErrApprovalRuntimeUnavailable = errors.New("очередь утверждения: исполнение недоступно")

// ApprovalQueue — запрос очереди утверждения: заявки тенанта, чьё исполнение
// стоит в фазе awaiting-approval.
type ApprovalQueue struct {
	TenantID domain.TenantID
}

// ApprovalItem — строка очереди утверждения: идентификаторы, оффер и наблюдаемое
// состояние исполнения. Строка не решает — она показывает. Target и Shortfall —
// цель по лидам и недобор: показываются только при явно заданной цели (Shortfall
// nil — цели нет, недобор не рассчитывается и не подменяется нулём).
type ApprovalItem struct {
	RequestID string
	TenantID  string
	Offer     OfferView
	Version   int
	Phase     string
	Execution string
	Found     int
	Target    int
	Shortfall *int
}

// ApprovalQueueHandler — use case чтения очереди утверждения.
type ApprovalQueueHandler struct {
	requests ports.RequestRepository
	status   ports.WorkflowStatusReader
}

// NewApprovalQueueHandler собирает use case очереди утверждения. Читатель
// состояния исполнения допускается равным nil: рантайм не подключён, и очередь
// честно отвечает исходом «исполнение недоступно».
func NewApprovalQueueHandler(requests ports.RequestRepository, status ports.WorkflowStatusReader) *ApprovalQueueHandler {
	return &ApprovalQueueHandler{requests: requests, status: status}
}

// Handle возвращает заявки, ожидающие утверждения: доменный статус processing,
// фаза по читателю состояния — awaiting-approval. Чужие тенанты не попадают в
// выборку: изоляция обеспечивается фильтром владельца, а не экраном.
func (h *ApprovalQueueHandler) Handle(ctx context.Context, q ApprovalQueue) ([]ApprovalItem, error) {
	if h.status == nil {
		return nil, ErrApprovalRuntimeUnavailable
	}
	requests, err := h.requests.ListByStatus(ctx, domain.StatusProcessing)
	if err != nil {
		return nil, err
	}
	items := make([]ApprovalItem, 0, len(requests))
	for _, request := range requests {
		if request.TenantID() != q.TenantID {
			continue
		}
		status, err := h.status.WorkflowStatus(ctx, request.ID().String())
		switch {
		case err == nil:
		case errors.Is(err, ports.ErrWorkflowNotFound):
			// Исполнения нет: заявка не стоит на паузе, в очередь не входит.
			continue
		case errors.Is(err, ports.ErrWorkflowUnavailable):
			return nil, fmt.Errorf("%w: %v", ErrApprovalRuntimeUnavailable, err)
		default:
			return nil, err
		}
		if status.Phase != ApprovalPhaseAwaitingApproval {
			continue
		}
		items = append(items, ApprovalItem{
			RequestID: request.ID().String(),
			TenantID:  request.TenantID().String(),
			Offer:     offerView(request.Offer()),
			Version:   request.Version(),
			Phase:     status.Phase,
			Execution: status.Execution,
			Found:     status.Found,
			Target:    status.Target,
			Shortfall: status.Shortfall,
		})
	}
	return items, nil
}
