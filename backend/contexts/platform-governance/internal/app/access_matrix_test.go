package app

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// roleSet собирает набор ролей для теста.
func roleSet(t *testing.T, roles ...domain.Role) domain.RoleSet {
	t.Helper()
	set, err := domain.NewRoleSet(roles...)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	return set
}

// TestAuthorizeSectionAdmin проверяет, что администратор платформы допущен во все
// разделы консоли.
func TestAuthorizeSectionAdmin(t *testing.T) {
	admin := roleSet(t, domain.RoleAdmin)
	cases := []struct {
		section Section
		action  Action
	}{
		{SectionUsers, ActionView},
		{SectionUsers, ActionManage},
		{SectionIntegrations, ActionView},
		{SectionTenant, ActionSwitch},
		{SectionAudit, ActionView},
		{SectionAudit, ActionExport},
		{SectionTopology, ActionView},
	}
	for _, c := range cases {
		if err := AuthorizeSection(admin, c.section, c.action); err != nil {
			t.Fatalf("администратор отвергнут на %s/%s: %v", c.section, c.action, err)
		}
	}
}

// TestAuthorizeSectionClientDenied проверяет, что клиентская роль не получает
// административных разделов: отказ решает матрица, а не навигация.
func TestAuthorizeSectionClientDenied(t *testing.T) {
	client := roleSet(t, domain.RoleClient)
	for _, c := range []struct {
		section Section
		action  Action
	}{
		{SectionUsers, ActionView},
		{SectionIntegrations, ActionView},
		{SectionAudit, ActionView},
		{SectionAudit, ActionExport},
		{SectionTenant, ActionSwitch},
		{SectionTopology, ActionView},
	} {
		if err := AuthorizeSection(client, c.section, c.action); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s/%s: ошибка = %v, ожидалась ErrForbidden", c.section, c.action, err)
		}
	}
}

// TestAuthorizeSectionEmptyDenied проверяет, что актор без ролей не получает
// доступа.
func TestAuthorizeSectionEmptyDenied(t *testing.T) {
	if err := AuthorizeSection(domain.RoleSet{}, SectionUsers, ActionView); !errors.Is(err, ErrForbidden) {
		t.Fatalf("пустой набор: ошибка = %v, ожидалась ErrForbidden", err)
	}
}

// TestAuthorizeSectionUnknownPair проверяет fail-closed: необъявленная пара
// «раздел → действие» запрещена, а не разрешена по умолчанию.
func TestAuthorizeSectionUnknownPair(t *testing.T) {
	admin := roleSet(t, domain.RoleAdmin)
	if err := AuthorizeSection(admin, Section("unknown"), ActionView); !errors.Is(err, ErrForbidden) {
		t.Fatalf("неизвестный раздел: ошибка = %v, ожидалась ErrForbidden", err)
	}
	if err := AuthorizeSection(admin, SectionAudit, Action("unknown")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("неизвестное действие: ошибка = %v, ожидалась ErrForbidden", err)
	}
}
