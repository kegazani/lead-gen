package access_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/access"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// Decider реализует порт проверки доступа.
var _ ports.AccessDecision = (*access.Decider)(nil)

func TestDeciderWithinTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	decider := access.NewDecider()
	if err := decider.Grant("operator-1", "tenant-1"); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	t.Run("право действует в своём тенанте", func(t *testing.T) {
		allowed, err := decider.CanAct(ctx, "operator-1", "tenant-1")
		if err != nil {
			t.Fatalf("CanAct: %v", err)
		}
		if !allowed {
			t.Fatal("ожидалось положительное решение внутри тенанта")
		}
	})

	t.Run("в чужом тенанте права нет", func(t *testing.T) {
		allowed, err := decider.CanAct(ctx, "operator-1", "tenant-2")
		if err != nil {
			t.Fatalf("CanAct: %v", err)
		}
		if allowed {
			t.Fatal("пара актор-тенант из другого тенанта не должна давать право")
		}
	})

	t.Run("неизвестный актор отвергается", func(t *testing.T) {
		allowed, err := decider.CanAct(ctx, "operator-2", "tenant-1")
		if err != nil {
			t.Fatalf("CanAct: %v", err)
		}
		if allowed {
			t.Fatal("актор без выданного права не должен получать доступ")
		}
	})

	t.Run("пустые актор и тенант — ошибка, а не отказ", func(t *testing.T) {
		if _, err := decider.CanAct(ctx, "", "tenant-1"); !errors.Is(err, access.ErrMissingSubject) {
			t.Fatalf("пустой актор: получено %v, ожидалась ErrMissingSubject", err)
		}
		if _, err := decider.CanAct(ctx, domain.ActorID("operator-1"), ""); !errors.Is(err, access.ErrMissingSubject) {
			t.Fatalf("пустой тенант: получено %v, ожидалась ErrMissingSubject", err)
		}
		if err := decider.Grant("", "tenant-1"); !errors.Is(err, access.ErrMissingSubject) {
			t.Fatalf("Grant с пустым актором: получено %v, ожидалась ErrMissingSubject", err)
		}
	})
}

func TestNewDeciderDeniesByDefault(t *testing.T) {
	t.Parallel()
	decider := access.NewDecider()
	allowed, err := decider.CanAct(context.Background(), "operator-1", "tenant-1")
	if err != nil {
		t.Fatalf("CanAct: %v", err)
	}
	if allowed {
		t.Fatal("пустой список разрешений не должен давать доступ")
	}
}
