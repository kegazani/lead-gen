package domain

import (
	"errors"
	"testing"
)

// TestNewRoleSetRejectsUnknown проверяет инвариант V1: набор принимает только
// объявленные роли.
func TestNewRoleSetRejectsUnknown(t *testing.T) {
	if _, err := NewRoleSet(Role("admin")); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownRole", err)
	}
}

// TestRoleSetWithWithoutImmutable проверяет инвариант V2: With и Without
// возвращают новый набор, прежний не меняется.
func TestRoleSetWithWithoutImmutable(t *testing.T) {
	base, err := NewRoleSet(RoleClient)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	extended, err := base.With(RoleAdmin)
	if err != nil {
		t.Fatalf("With: %v", err)
	}
	if base.Has(RoleAdmin) {
		t.Fatal("прежний набор изменился при With")
	}
	if !extended.Has(RoleAdmin) || !extended.Has(RoleClient) {
		t.Fatalf("расширенный набор = %+v", extended.Roles())
	}
	shrunk, err := extended.Without(RoleAdmin)
	if err != nil {
		t.Fatalf("Without: %v", err)
	}
	if !extended.Has(RoleAdmin) {
		t.Fatal("прежний набор изменился при Without")
	}
	if shrunk.Has(RoleAdmin) || !shrunk.Has(RoleClient) {
		t.Fatalf("урезанный набор = %+v", shrunk.Roles())
	}
}

// TestRoleSetNoDuplicates проверяет инвариант V3: повтор роли не создаёт дубль.
func TestRoleSetNoDuplicates(t *testing.T) {
	set, err := NewRoleSet(RoleClient, RoleClient, RoleAdmin)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	if set.Len() != 2 {
		t.Fatalf("ролей = %d, ожидалось 2", set.Len())
	}
}

// TestRoleSetWithIdempotent проверяет, что добавление имеющейся роли не меняет
// набор.
func TestRoleSetWithIdempotent(t *testing.T) {
	base, err := NewRoleSet(RoleClient)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	same, err := base.With(RoleClient)
	if err != nil {
		t.Fatalf("With: %v", err)
	}
	if !same.Equal(base) {
		t.Fatal("повторное добавление роли изменило набор")
	}
}
