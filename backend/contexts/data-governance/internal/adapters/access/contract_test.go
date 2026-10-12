package access_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/data-governance/internal/adapters/access"
	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
	"lead-gen/contracts/platform-governance/accessdecision"
)

// Contract реализует порт потребителя.
var _ ports.AccessDecision = (*access.Contract)(nil)

type stubDecider struct {
	allowed   bool
	err       error
	gotActor  string
	gotTenant string
}

func (s *stubDecider) CanAct(_ context.Context, actor, tenant string) (bool, error) {
	s.gotActor, s.gotTenant = actor, tenant
	return s.allowed, s.err
}

func TestContractTranslatesTypesAndPassesDecision(t *testing.T) {
	t.Parallel()
	stub := &stubDecider{allowed: true}
	contract, err := access.NewContract(stub)
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	allowed, err := contract.CanAct(context.Background(), "operator-1", "tenant-1")
	if err != nil || !allowed {
		t.Fatalf("CanAct = %v, %v", allowed, err)
	}
	if stub.gotActor != "operator-1" || stub.gotTenant != "tenant-1" {
		t.Fatalf("типы переведены неверно: actor=%q tenant=%q", stub.gotActor, stub.gotTenant)
	}
}

func TestContractRejectsEmptySubject(t *testing.T) {
	t.Parallel()
	contract, err := access.NewContract(&stubDecider{allowed: true})
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	if _, err := contract.CanAct(context.Background(), "", "tenant-1"); !errors.Is(err, access.ErrMissingSubject) {
		t.Fatalf("пустой актор: ошибка = %v, ожидалась ErrMissingSubject", err)
	}
	if _, err := contract.CanAct(context.Background(), domain.ActorID("operator-1"), ""); !errors.Is(err, access.ErrMissingSubject) {
		t.Fatalf("пустой тенант: ошибка = %v, ожидалась ErrMissingSubject", err)
	}
}

func TestContractPropagatesUnavailable(t *testing.T) {
	t.Parallel()
	contract, err := access.NewContract(accessdecision.Unavailable{})
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	allowed, err := contract.CanAct(context.Background(), "operator-1", "tenant-1")
	if allowed {
		t.Fatal("недоступность владельца не должна давать разрешение")
	}
	if !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

func TestNewContractRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := access.NewContract(nil); err == nil {
		t.Fatal("nil-реализация контракта должна отвергаться")
	}
}
