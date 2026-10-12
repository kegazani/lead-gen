package httpapi

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

type workflowCancellerFunc func(context.Context, string) error

func (f workflowCancellerFunc) CancelWorkflow(ctx context.Context, requestID string) error {
	return f(ctx, requestID)
}

func TestRequestWorkflowCancellationDistinguishesAcceptedMissingAndUnavailable(t *testing.T) {
	t.Run("исполнение отменено", func(t *testing.T) {
		calls := 0
		outreachCalls := 0
		server := &Server{deps: Dependencies{
			WorkflowCanceller: workflowCancellerFunc(func(_ context.Context, requestID string) error {
				calls++
				if requestID != "req-1" {
					t.Fatalf("requestID = %q", requestID)
				}
				return nil
			}),
			OutreachCancellation: outreachhandoff.InProc(outreachhandoff.InProcHandler{CancelFunc: func(_ context.Context, cancellation outreachhandoff.RequestCancellation) error {
				outreachCalls++
				if cancellation.TenantID != "tenant-1" || cancellation.RequestID != "req-1" {
					t.Fatalf("cancellation = %+v", cancellation)
				}
				return nil
			}}),
		}}
		if err := server.requestWorkflowCancellation(context.Background(), "tenant-1", "req-1"); err != nil || calls != 1 || outreachCalls != 1 {
			t.Fatalf("cancel err=%v Temporal calls=%d outreach calls=%d", err, calls, outreachCalls)
		}
	})

	t.Run("workflow ещё не запускался", func(t *testing.T) {
		server := &Server{deps: Dependencies{
			WorkflowCanceller:    workflowCancellerFunc(func(context.Context, string) error { return ports.ErrWorkflowNotFound }),
			OutreachCancellation: outreachhandoff.InProc(outreachhandoff.InProcHandler{CancelFunc: func(context.Context, outreachhandoff.RequestCancellation) error { return nil }}),
		}}
		if err := server.requestWorkflowCancellation(context.Background(), "tenant-1", "req-1"); err != nil {
			t.Fatalf("отмена заявки без запущенного workflow = %v", err)
		}
	})

	t.Run("Temporal недоступен", func(t *testing.T) {
		server := &Server{deps: Dependencies{
			WorkflowCanceller:    workflowCancellerFunc(func(context.Context, string) error { return ports.ErrWorkflowUnavailable }),
			OutreachCancellation: outreachhandoff.InProc(outreachhandoff.InProcHandler{CancelFunc: func(context.Context, outreachhandoff.RequestCancellation) error { return nil }}),
		}}
		if err := server.requestWorkflowCancellation(context.Background(), "tenant-1", "req-1"); !errors.Is(err, ErrWorkflowCancellationPending) {
			t.Fatalf("ожидался видимый pending, получено %v", err)
		}
	})
}
