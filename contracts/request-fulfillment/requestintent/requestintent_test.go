package requestintent_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/request-fulfillment/requestintent"
)

// TestInProcDelegates проверяет, что прямой адаптер передаёт команду и запрос
// обработчикам и возвращает их результат.
func TestInProcDelegates(t *testing.T) {
	var gotSubmit requestintent.SubmitIntent
	var gotStatus requestintent.StatusQuery
	port := requestintent.InProc(requestintent.InProcHandler{
		SubmitFunc: func(_ context.Context, in requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
			gotSubmit = in
			return requestintent.SubmitResult{RequestID: in.RequestID, Status: "submitted"}, nil
		},
		StatusFunc: func(_ context.Context, q requestintent.StatusQuery) (requestintent.StatusResult, error) {
			gotStatus = q
			return requestintent.StatusResult{RequestID: q.RequestID, Status: "submitted"}, nil
		},
	})

	result, err := port.Submit(context.Background(), requestintent.SubmitIntent{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1", Content: "Оффер",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.RequestID != "req-1" || result.Status != "submitted" {
		t.Fatalf("Submit результат = %+v", result)
	}
	if gotSubmit.Content != "Оффер" || gotSubmit.TenantID != "tenant-1" {
		t.Fatalf("обработчик Submit получил %+v", gotSubmit)
	}

	status, err := port.Status(context.Background(), requestintent.StatusQuery{RequestID: "req-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.RequestID != "req-1" || status.Status != "submitted" {
		t.Fatalf("Status результат = %+v", status)
	}
	if gotStatus.RequestID != "req-1" {
		t.Fatalf("обработчик Status получил %+v", gotStatus)
	}
}

// TestInProcNilHandlersUnavailable проверяет, что незаданные обработчики честно
// сообщают о недоступности владельца, а не выдают пустой успех.
func TestInProcNilHandlersUnavailable(t *testing.T) {
	port := requestintent.InProc(requestintent.InProcHandler{})
	if _, err := port.Submit(context.Background(), requestintent.SubmitIntent{}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Submit ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := port.Status(context.Background(), requestintent.StatusQuery{}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Status ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestInProcPropagatesDomainErrors проверяет, что доменные исходы владельца
// доходят до потребителя без подмены успехом.
func TestInProcPropagatesDomainErrors(t *testing.T) {
	port := requestintent.InProc(requestintent.InProcHandler{
		SubmitFunc: func(context.Context, requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
			return requestintent.SubmitResult{}, requestintent.ErrRejected
		},
		StatusFunc: func(context.Context, requestintent.StatusQuery) (requestintent.StatusResult, error) {
			return requestintent.StatusResult{}, requestintent.ErrNotFound
		},
	})
	if _, err := port.Submit(context.Background(), requestintent.SubmitIntent{}); !errors.Is(err, requestintent.ErrRejected) {
		t.Fatalf("Submit ошибка = %v, ожидалась ErrRejected", err)
	}
	if _, err := port.Status(context.Background(), requestintent.StatusQuery{}); !errors.Is(err, requestintent.ErrNotFound) {
		t.Fatalf("Status ошибка = %v, ожидалась ErrNotFound", err)
	}
}
