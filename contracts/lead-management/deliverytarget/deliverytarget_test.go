package deliverytarget

import (
	"context"
	"errors"
	"testing"
)

func TestInProcHandler_WithoutFuncIsUnavailable(t *testing.T) {
	// Незаданная реализация владельца — честная недоступность, а не пустой адрес.
	resolver := InProc(InProcHandler{})
	if _, err := resolver.ResolveRecipient(context.Background(), "tenant-1", "ref-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, got %v", err)
	}
}

func TestInProcHandler_DelegatesAndPassesArguments(t *testing.T) {
	var gotTenant, gotRef string
	resolver := InProc(InProcHandler{
		ResolveFunc: func(_ context.Context, tenantID, recipientRef string) (Recipient, error) {
			gotTenant, gotRef = tenantID, recipientRef
			return Recipient{Address: "lead@example.com"}, nil
		},
	})
	recipient, err := resolver.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if recipient.Address != "lead@example.com" {
		t.Fatalf("адрес: %q", recipient.Address)
	}
	if gotTenant != "tenant-1" || gotRef != "ref-1" {
		t.Fatalf("аргументы не переданы: tenant=%q ref=%q", gotTenant, gotRef)
	}
}

func TestInProcHandler_PropagatesRejection(t *testing.T) {
	resolver := InProc(InProcHandler{
		ResolveFunc: func(context.Context, string, string) (Recipient, error) {
			return Recipient{}, ErrRejected
		},
	})
	if _, err := resolver.ResolveRecipient(context.Background(), "tenant-1", "ref-1"); !errors.Is(err, ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, got %v", err)
	}
}

func TestContractIdentity(t *testing.T) {
	if ContractName != "delivery-target" {
		t.Fatalf("ContractName = %q", ContractName)
	}
	if SchemaVersion != "1.0.0" {
		t.Fatalf("SchemaVersion = %q", SchemaVersion)
	}
}
