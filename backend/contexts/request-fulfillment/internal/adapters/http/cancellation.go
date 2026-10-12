package httpapi

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

// ErrWorkflowCancellationPending означает, что состояние заявки durable
// переведено в cancelled, но Temporal пока не подтвердил запрос остановки.
// Повтор той же команды безопасен и повторяет доставку запроса.
var ErrWorkflowCancellationPending = errors.New("исполнение заявки ожидает подтверждения отмены")

func (s *Server) requestWorkflowCancellation(ctx context.Context, tenantID, requestID string) error {
	var failures []error
	if s.deps.OutreachCancellation != nil {
		if err := s.deps.OutreachCancellation.CancelRequest(ctx, outreachhandoff.RequestCancellation{
			TenantID: tenantID, RequestID: requestID,
		}); err != nil {
			failures = append(failures, err)
		}
	} else if s.deps.WorkflowCanceller != nil {
		failures = append(failures, errors.New("outreach cancellation port is not wired"))
	}
	if s.deps.WorkflowCanceller != nil {
		if err := s.deps.WorkflowCanceller.CancelWorkflow(ctx, requestID); err != nil && !errors.Is(err, ports.ErrWorkflowNotFound) {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w: %v", ErrWorkflowCancellationPending, errors.Join(failures...))
	}
	return nil
}
