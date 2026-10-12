package platformgovernance_test

import (
	"context"
	"errors"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/contracts/platform-governance/accessdecision"
)

// TestNewAccessDecisionWithoutSliceIsUnavailable проверяет, что несобранный
// владелец не выдаёт разрешение: потребитель получает честный отказ.
func TestNewAccessDecisionWithoutSliceIsUnavailable(t *testing.T) {
	t.Parallel()
	decider := platformgovernance.NewAccessDecision(nil)
	allowed, err := decider.CanAct(context.Background(), "user-1", "tenant-1")
	if allowed {
		t.Fatal("несобранный владелец не должен выдавать разрешение")
	}
	if !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewAccessDecisionReflectsDisable проверяет сквозной сценарий через
// контракт: после отключения тенанта решение становится отрицательным.
func TestNewAccessDecisionReflectsDisable(t *testing.T) {
	t.Parallel()
	slice := newSlice(t)
	createTenant(t, slice, "cmd-create", "tenant-1")
	decider := platformgovernance.NewAccessDecision(slice)
	if allowed, err := decider.CanAct(context.Background(), "user-1", "tenant-1"); err != nil || !allowed {
		t.Fatalf("CanAct до отключения = %v, %v", allowed, err)
	}
	if err := slice.DisableTenant.Handle(context.Background(), command.DisableTenant{
		Actor: "admin-1", CommandID: "cmd-disable", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("DisableTenant: %v", err)
	}
	if allowed, err := decider.CanAct(context.Background(), "user-1", "tenant-1"); err != nil || allowed {
		t.Fatalf("CanAct после отключения = %v, %v, ожидалось false", allowed, err)
	}
}

// TestNewAccessDecisionDelegatesToOwner проверяет привязку контракта к решению
// владельца: положительное решение члену тенанта, отрицательное — постороннему,
// ошибка — пустому субъекту.
func TestNewAccessDecisionDelegatesToOwner(t *testing.T) {
	t.Parallel()
	slice := newSlice(t)
	createTenant(t, slice, "cmd-create", "tenant-1")
	decider := platformgovernance.NewAccessDecision(slice)

	allowed, err := decider.CanAct(context.Background(), "user-1", "tenant-1")
	if err != nil || !allowed {
		t.Fatalf("CanAct члена тенанта = %v, %v", allowed, err)
	}
	allowed, err = decider.CanAct(context.Background(), "stranger", "tenant-1")
	if err != nil || allowed {
		t.Fatalf("CanAct постороннего = %v, %v", allowed, err)
	}
	if _, err := decider.CanAct(context.Background(), "", "tenant-1"); err == nil {
		t.Fatal("пустой актор должен давать ошибку, а не решение")
	}
}
