package inproc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/contracts/platform-governance/accessdecision"
	"lead-gen/contracts/platform-governance/accessdecision/inproc"
)

type deciderFunc func(ctx context.Context, actor, tenant string) (bool, error)

func (f deciderFunc) CanAct(ctx context.Context, actor, tenant string) (bool, error) {
	return f(ctx, actor, tenant)
}

func TestBindRejectsMissingOwnerImpl(t *testing.T) {
	t.Parallel()
	if _, err := inproc.Bind(nil); err == nil {
		t.Fatal("nil-реализация владельца должна отвергаться")
	}
}

func TestBindPassesThroughPositiveAndNegative(t *testing.T) {
	t.Parallel()
	bound, err := inproc.Bind(deciderFunc(func(context.Context, string, string) (bool, error) {
		return false, nil
	}))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	// Адаптер не подменяет решение владельца: отрицательное остаётся отрицательным.
	allowed, err := bound.CanAct(context.Background(), "operator-1", "tenant-1")
	if err != nil {
		t.Fatalf("CanAct: %v", err)
	}
	if allowed {
		t.Fatal("адаптер выдал разрешение вместо решения владельца")
	}

	positive, err := inproc.Bind(deciderFunc(func(context.Context, string, string) (bool, error) {
		return true, nil
	}))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if allowed, err = positive.CanAct(context.Background(), "operator-1", "tenant-1"); err != nil || !allowed {
		t.Fatalf("положительное решение владельца не проброшено: allowed=%v err=%v", allowed, err)
	}
}

func TestBindPropagatesOwnerError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("владелец недоступен")
	bound, err := inproc.Bind(deciderFunc(func(context.Context, string, string) (bool, error) {
		return false, sentinel
	}))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	allowed, err := bound.CanAct(context.Background(), "operator-1", "tenant-1")
	if allowed {
		t.Fatal("ошибка владельца не должна давать разрешение")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("ошибка владельца не проброшена: %v", err)
	}
}

func TestBindEnforcesDeadline(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	bound, err := inproc.Bind(deciderFunc(func(ctx context.Context, _, _ string) (bool, error) {
		select {
		case <-block:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}), inproc.WithTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	allowed, err := bound.CanAct(context.Background(), "operator-1", "tenant-1")
	if allowed {
		t.Fatal("по истечении бюджета решение не должно быть положительным")
	}
	if err == nil {
		t.Fatal("ожидалась ошибка истечения бюджета")
	}
}

func TestAdapterSatisfiesContract(t *testing.T) {
	t.Parallel()
	var _ accessdecision.Decider = (*inproc.Adapter)(nil)
}
