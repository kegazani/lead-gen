package readsignal

import (
	"context"
	"errors"
	"testing"
	"time"
)

// resolverStub — минимальная реализация обеих операций контракта.
type resolverStub struct{}

func (resolverStub) Resolve(_ context.Context, ref LinkRef) (string, error) {
	if ref == "unknown" {
		return "", ErrNotFound
	}
	return "https://example.com/", nil
}

func (resolverStub) Record(_ context.Context, _ Transition) error { return nil }

func TestContractIdentity(t *testing.T) {
	if ContractName != "read-signal" {
		t.Errorf("ContractName: получено %q", ContractName)
	}
	if SchemaVersion == "" {
		t.Error("пустая SchemaVersion")
	}
}

func TestResolverContract(t *testing.T) {
	var resolver Resolver = resolverStub{}
	target, err := resolver.Resolve(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Resolve: неожиданная ошибка: %v", err)
	}
	if target == "" {
		t.Fatal("пустой адрес перехода")
	}
	if _, err := resolver.Resolve(context.Background(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено %v", err)
	}
}

func TestRecorderContract(t *testing.T) {
	var recorder Recorder = resolverStub{}
	transition := Transition{Link: "demo", Target: "https://example.com/", OccurredAt: time.Now().UTC()}
	if err := recorder.Record(context.Background(), transition); err != nil {
		t.Fatalf("Record: неожиданная ошибка: %v", err)
	}
}
