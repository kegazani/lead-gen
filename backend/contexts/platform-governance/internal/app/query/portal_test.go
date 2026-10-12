package query

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/platform-governance/internal/app/projection"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// TestPortalStatePropagatesFailureReason проверяет, что причина сбоя из контракта
// request-intent доходит до PortalView: код и текст причины доступны порталу, а не
// отбрасываются проекцией.
func TestPortalStatePropagatesFailureReason(t *testing.T) {
	proj := projection.NewLastRequest()
	proj.Remember(domain.TenantID("tenant-1"), "req-f")
	handler := NewPortalStateHandler(requestintent.InProc(requestintent.InProcHandler{
		StatusFunc: func(_ context.Context, q requestintent.StatusQuery) (requestintent.StatusResult, error) {
			if q.RequestID != "req-f" || q.TenantID != "tenant-1" {
				t.Fatalf("StatusQuery = %+v, ожидалась req-f/tenant-1", q)
			}
			return requestintent.StatusResult{
				RequestID: "req-f", Status: "failed", Phase: "failed",
				ErrorReason: "search_failed", ErrorMessage: "поиск упал",
			}, nil
		},
	}), proj)

	view, err := handler.Handle(context.Background(), PortalState{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !view.HasRequest || view.RequestID != "req-f" || view.Status != "failed" {
		t.Fatalf("вид = %+v, ожидалась упавшая заявка req-f", view)
	}
	if view.ErrorReason != "search_failed" || view.ErrorMessage != "поиск упал" {
		t.Fatalf("причина = %q/%q, ожидалась search_failed/поиск упал", view.ErrorReason, view.ErrorMessage)
	}
}
