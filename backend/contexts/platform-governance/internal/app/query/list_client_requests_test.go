package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// TestListClientRequestsMapsEntries проверяет, что сценарий отдаёт список заявок
// клиента в порядке владельца: идентификатор и текущий статус, без выдуманных
// полей.
func TestListClientRequestsMapsEntries(t *testing.T) {
	handler := NewListClientRequestsHandler(requestintent.InProc(requestintent.InProcHandler{
		ListFunc: func(_ context.Context, q requestintent.ListQuery) ([]requestintent.ListEntry, error) {
			if q.TenantID != "tenant-1" {
				t.Fatalf("TenantID = %q, ожидался tenant-1", q.TenantID)
			}
			return []requestintent.ListEntry{
				{RequestID: "req-2", Status: "processing"},
				{RequestID: "req-1", Status: "submitted"},
			}, nil
		},
	}))

	views, err := handler.Handle(context.Background(), ListClientRequests{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("строк = %d, ожидалось 2", len(views))
	}
	if views[0].RequestID != "req-2" || views[0].Status != "processing" {
		t.Fatalf("первая строка = %+v", views[0])
	}
	if views[1].RequestID != "req-1" || views[1].Status != "submitted" {
		t.Fatalf("вторая строка = %+v", views[1])
	}
}

// TestListClientRequestsEmpty проверяет, что отсутствие заявок — пустой список, а
// не ошибка.
func TestListClientRequestsEmpty(t *testing.T) {
	handler := NewListClientRequestsHandler(requestintent.InProc(requestintent.InProcHandler{
		ListFunc: func(context.Context, requestintent.ListQuery) ([]requestintent.ListEntry, error) {
			return []requestintent.ListEntry{}, nil
		},
	}))
	views, err := handler.Handle(context.Background(), ListClientRequests{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("строк = %d, ожидалось 0", len(views))
	}
}

// TestListClientRequestsUnavailable проверяет, что недоступность владельца не
// маскируется пустым успехом: исход сценария — ErrUnavailable.
func TestListClientRequestsUnavailable(t *testing.T) {
	handler := NewListClientRequestsHandler(requestintent.InProc(requestintent.InProcHandler{}))
	if _, err := handler.Handle(context.Background(), ListClientRequests{TenantID: "tenant-1"}); !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
