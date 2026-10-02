package callintent_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/outreach-orchestration/callintent"
)

// TestInProcCallsHandler проверяет, что прямой адаптер передаёт команду
// обработчику и возвращает его результат.
func TestInProcCallsHandler(t *testing.T) {
	var got callintent.Intent
	port := callintent.InProc(func(_ context.Context, intent callintent.Intent) (callintent.Result, error) {
		got = intent
		return callintent.Result{CallID: "call-1"}, nil
	})

	result, err := port.Initiate(context.Background(), callintent.Intent{
		RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1",
		Purpose: callintent.PurposeOfferClarification,
	})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	if result.CallID != "call-1" {
		t.Fatalf("call_id = %q", result.CallID)
	}
	if got.RequestID != "req-1" || got.Purpose != callintent.PurposeOfferClarification {
		t.Fatalf("обработчик получил %+v", got)
	}
}

// TestInProcPropagatesRejection проверяет, что доменный отказ владельца доходит до
// потребителя без подмены успехом.
func TestInProcPropagatesRejection(t *testing.T) {
	port := callintent.InProc(func(context.Context, callintent.Intent) (callintent.Result, error) {
		return callintent.Result{}, callintent.ErrRejected
	})
	if _, err := port.Initiate(context.Background(), callintent.Intent{}); !errors.Is(err, callintent.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}
