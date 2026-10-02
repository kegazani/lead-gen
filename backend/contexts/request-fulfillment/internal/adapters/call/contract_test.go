package call

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/outreach-orchestration/callintent"
)

// TestContractMapsSuccess проверяет перевод успешного ответа контракта в порт.
func TestContractMapsSuccess(t *testing.T) {
	adapter := NewContract(callintent.InProc(func(context.Context, callintent.Intent) (callintent.Result, error) {
		return callintent.Result{CallID: "call-1"}, nil
	}))
	result, err := adapter.InitiateCall(context.Background(), ports.InitiateCall{RequestID: "req-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("InitiateCall: %v", err)
	}
	if result.CallID != "call-1" {
		t.Fatalf("call_id = %q", result.CallID)
	}
}

// TestContractMapsRejection проверяет перевод доменного отказа владельца.
func TestContractMapsRejection(t *testing.T) {
	adapter := NewContract(callintent.InProc(func(context.Context, callintent.Intent) (callintent.Result, error) {
		return callintent.Result{}, callintent.ErrRejected
	}))
	if _, err := adapter.InitiateCall(context.Background(), ports.InitiateCall{}); !errors.Is(err, ports.ErrCallRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrCallRejected", err)
	}
}

// TestContractMapsFailure проверяет перевод сбоя контракта.
func TestContractMapsFailure(t *testing.T) {
	adapter := NewContract(callintent.InProc(func(context.Context, callintent.Intent) (callintent.Result, error) {
		return callintent.Result{}, errors.New("сеть недоступна")
	}))
	if _, err := adapter.InitiateCall(context.Background(), ports.InitiateCall{}); !errors.Is(err, ports.ErrCallUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrCallUnavailable", err)
	}
}

// TestContractRejectsEmptyCallID проверяет, что пустой идентификатор звонка от
// владельца не принимается за успех.
func TestContractRejectsEmptyCallID(t *testing.T) {
	adapter := NewContract(callintent.InProc(func(context.Context, callintent.Intent) (callintent.Result, error) {
		return callintent.Result{}, nil
	}))
	if _, err := adapter.InitiateCall(context.Background(), ports.InitiateCall{}); !errors.Is(err, ports.ErrCallUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrCallUnavailable", err)
	}
}

// TestContractWithoutPort проверяет отказ адаптера без подключённого контракта.
func TestContractWithoutPort(t *testing.T) {
	if _, err := NewContract(nil).InitiateCall(context.Background(), ports.InitiateCall{}); !errors.Is(err, ports.ErrCallUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrCallUnavailable", err)
	}
}

// TestUnavailableRejects проверяет честный отказ недоступного адаптера.
func TestUnavailableRejects(t *testing.T) {
	if _, err := (Unavailable{}).InitiateCall(context.Background(), ports.InitiateCall{}); !errors.Is(err, ports.ErrCallUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrCallUnavailable", err)
	}
}
