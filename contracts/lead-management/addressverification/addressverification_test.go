package addressverification

import (
	"context"
	"errors"
	"testing"
)

func TestQueryValidate(t *testing.T) {
	valid := Query{TenantID: "tenant-1", RecipientRef: "lead-1"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	for _, invalid := range []Query{
		{RecipientRef: "lead-1"},
		{TenantID: "tenant-1"},
		{TenantID: "  ", RecipientRef: "lead-1"},
		{},
	} {
		if err := invalid.Validate(); !errors.Is(err, ErrRejected) {
			t.Fatalf("некорректный запрос %+v: ошибка = %v, ожидалась ErrRejected", invalid, err)
		}
	}
}

func TestQueryIdempotencyKeyDistinct(t *testing.T) {
	first := Query{TenantID: "tenant-1", RecipientRef: "lead-1"}
	if first.IdempotencyKey() != (Query{TenantID: "tenant-1", RecipientRef: "lead-1"}).IdempotencyKey() {
		t.Fatal("одинаковый запрос дал разные ключи идемпотентности")
	}
	other := Query{TenantID: "tenant-1", RecipientRef: "lead-2"}
	if first.IdempotencyKey() == other.IdempotencyKey() {
		t.Fatal("разные получатели дали одинаковый ключ идемпотентности")
	}
	if first.IdempotencyKey() == (Query{TenantID: "tenant-2", RecipientRef: "lead-1"}).IdempotencyKey() {
		t.Fatal("разные тенанты дали одинаковый ключ идемпотентности")
	}
}

func TestInProcHandlerWithoutFuncIsUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Verification(context.Background(), Query{TenantID: "t", RecipientRef: "r"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, got %v", err)
	}
}

func TestInProcHandlerDelegates(t *testing.T) {
	port := InProc(InProcHandler{
		VerificationFunc: func(_ context.Context, q Query) (Snapshot, error) {
			return Snapshot{RecipientRef: q.RecipientRef, Status: StatusDeliverable, Verified: true}, nil
		},
	})
	snapshot, err := port.Verification(context.Background(), Query{TenantID: "t", RecipientRef: "r"})
	if err != nil {
		t.Fatalf("Verification: %v", err)
	}
	if snapshot.RecipientRef != "r" || !snapshot.Verified {
		t.Fatalf("снимок = %+v", snapshot)
	}
}

func TestStatusKnown(t *testing.T) {
	for _, status := range []Status{
		StatusDeliverable, StatusRisky, StatusUnknown, StatusInvalid,
		StatusDisposable, StatusRole, StatusAbuse,
	} {
		if !status.Known() {
			t.Errorf("статус %q должен быть известен", status)
		}
	}
	if Status("выдумка").Known() {
		t.Fatal("незнакомый статус не должен считаться известным")
	}
}

func TestContractIdentity(t *testing.T) {
	if ContractName != "address-verification" {
		t.Fatalf("ContractName = %q", ContractName)
	}
	if SchemaVersion != "1.0.0" {
		t.Fatalf("SchemaVersion = %q", SchemaVersion)
	}
}
