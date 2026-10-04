package grants_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// TestRoles проверяет выдачу ролей актору и пустой набор для неизвестного.
func TestRoles(t *testing.T) {
	table, err := grants.FromStrings("admin-1", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("FromStrings: %v", err)
	}
	roles, err := table.Roles(context.Background(), "admin-1")
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if !roles.Has(domain.RoleAdmin) {
		t.Fatalf("роли = %+v", roles.Roles())
	}
	unknown, err := table.Roles(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("Roles неизвестного: %v", err)
	}
	if !unknown.Empty() {
		t.Fatalf("неизвестный актор получил роли: %+v", unknown.Roles())
	}
}

// TestFromStringsRejectsUnknownRole проверяет, что неизвестная роль — ошибка
// сборки, а не тихий отказ.
func TestFromStringsRejectsUnknownRole(t *testing.T) {
	if _, err := grants.FromStrings("admin-1", []string{"superuser"}); !errors.Is(err, domain.ErrUnknownRole) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownRole", err)
	}
}
