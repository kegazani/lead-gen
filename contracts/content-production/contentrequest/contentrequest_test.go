package contentrequest

import (
	"context"
	"errors"
	"testing"
)

func TestContractIdentity(t *testing.T) {
	if ContractName != "content-request" {
		t.Errorf("ContractName: получено %q", ContractName)
	}
	if SchemaVersion == "" {
		t.Error("пустая SchemaVersion")
	}
}

func TestInProcUnavailableWithoutFunc(t *testing.T) {
	_, err := InProc(InProcHandler{}).RequestContent(context.Background(), Intent{RequestID: "r1"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestInProcDelegates(t *testing.T) {
	port := InProc(InProcHandler{
		RequestFunc: func(_ context.Context, in Intent) (Result, error) {
			return Result{RequestID: in.RequestID, AssetID: in.AssetID, State: "requested"}, nil
		},
	})
	result, err := port.RequestContent(context.Background(), Intent{RequestID: "r1", AssetID: "a1"})
	if err != nil {
		t.Fatalf("RequestContent: %v", err)
	}
	if result.RequestID != "r1" || result.AssetID != "a1" || result.State != "requested" {
		t.Fatalf("результат неполон: %+v", result)
	}
}
