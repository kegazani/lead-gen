package workflow

import "testing"

// TestRegistryCollectsRegistrations проверяет, что реестр сохраняет workflow и
// activities с явными именами.
func TestRegistryCollectsRegistrations(t *testing.T) {
	registry := NewRegistry()
	if !registry.Empty() {
		t.Fatal("новый реестр должен быть пуст")
	}
	registry.RegisterWorkflowNamed("fulfillment", validPayload{})
	registry.RegisterActivity(func() {})
	if registry.Empty() {
		t.Fatal("реестр с регистрациями не должен быть пуст")
	}
	if got := registry.Workflows(); len(got) != 1 || got[0].Name != "fulfillment" {
		t.Fatalf("workflow зарегистрирован неверно: %+v", got)
	}
	if got := registry.Activities(); len(got) != 1 || got[0].Name != "" {
		t.Fatalf("activity зарегистрирована неверно: %+v", got)
	}
}

// TestRegistryReturnsCopy проверяет, что списки реестра — копии.
func TestRegistryReturnsCopy(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterWorkflowNamed("a", validPayload{})
	workflows := registry.Workflows()
	workflows[0].Name = "mutated"
	if registry.Workflows()[0].Name != "a" {
		t.Fatal("изменение возвращённого слайса не должно влиять на реестр")
	}
}
