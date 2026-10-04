package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// consoleFakeCatalog — управляемый порт чтения адаптеров для тестов консоли.
type consoleFakeCatalog struct {
	states []ports.AdapterState
}

// States реализует ports.AdapterCatalog.
func (f consoleFakeCatalog) States(context.Context) ([]ports.AdapterState, error) {
	return f.states, nil
}

// newConsoleServer собирает сервер консоли с администратором user-1 в тенанте
// tenant-1 и клиентом user-3 в тенанте tenant-2.
func newConsoleServer(t *testing.T, adapters ports.AdapterCatalog) *httptest.Server {
	t.Helper()
	grantsTable, err := grants.New(map[string][]string{
		"admin-1": {string(domain.RoleAdmin)},
		"user-1":  {string(domain.RoleAdmin)},
	})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{Grants: grantsTable, Adapters: adapters})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	createTenantForConsole(t, slice, "cmd-1", "tenant-1", "user-1", []string{string(domain.RoleAdmin)})
	createTenantForConsole(t, slice, "cmd-2", "tenant-2", "user-3", []string{string(domain.RoleClient)})

	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// createTenantForConsole создаёт тенант консоли с одним пользователем.
func createTenantForConsole(t *testing.T, slice *platformgovernance.Slice, commandID, tenantID, userID string, roles []string) {
	t.Helper()
	if _, err := slice.CreateTenant.Handle(context.Background(), command.CreateTenant{
		Actor:       "admin-1",
		CommandID:   commandID,
		TenantID:    tenantID,
		CompanyName: "ООО Ромашка",
		Description: "Поставщик промышленного оборудования",
		UserID:      userID,
		Login:       "login-" + userID,
		Roles:       roles,
	}); err != nil {
		t.Fatalf("CreateTenant %s: %v", tenantID, err)
	}
}

// TestConsoleUsersListsRolesFromOwner проверяет, что экран показывает логин и роли
// пользователя, полученные от владельца модели.
func TestConsoleUsersListsRolesFromOwner(t *testing.T) {
	server := newConsoleServer(t, nil)
	page := body(t, get(t, server, "/console/users?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, "login-user-1") || !strings.Contains(page, string(domain.RoleAdmin)) {
		t.Fatalf("экран не показал пользователя и его роль:\n%s", page)
	}
}

// TestConsoleUsersDeniedForNonAdmin проверяет, что неадминистратор получает отказ
// на сервере, а не пустую страницу.
func TestConsoleUsersDeniedForNonAdmin(t *testing.T) {
	server := newConsoleServer(t, nil)
	resp := get(t, server, "/console/users?tenant_id=tenant-1&actor=stranger")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой актор = %d, ожидался 403", resp.StatusCode)
	}
}

// TestConsoleDeniedIndependentOfNavigation проверяет, что отказ не зависит от
// видимости пункта навигации: клиентская роль отвергается на эндпоинте.
func TestConsoleDeniedIndependentOfNavigation(t *testing.T) {
	server := newConsoleServer(t, nil)
	for _, path := range []string{
		"/console/audit?tenant_id=tenant-2&actor=user-3",
		"/console/audit/export?tenant_id=tenant-2&actor=user-3",
		"/console/integrations?tenant_id=tenant-2&actor=user-3",
	} {
		resp := get(t, server, path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s = %d, ожидался 403", path, resp.StatusCode)
		}
	}
}

// TestConsoleSwitchTenantDenied проверяет, что переключение на недоступный тенант
// запрещено на сервере, а на доступный — разрешено с возвратом (PRG).
func TestConsoleSwitchTenantDenied(t *testing.T) {
	server := newConsoleServer(t, nil)
	resp := postForm(t, server, "/console/tenant/switch", url.Values{
		"tenant_id": {"tenant-2"},
		"actor":     {"user-1"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("переключение на чужой тенант = %d, ожидался 403", resp.StatusCode)
	}

	allowed := postForm(t, server, "/console/tenant/switch", url.Values{
		"tenant_id": {"tenant-1"},
		"actor":     {"user-1"},
	})
	defer allowed.Body.Close()
	if allowed.StatusCode != http.StatusSeeOther {
		t.Fatalf("переключение на свой тенант = %d, ожидался 303", allowed.StatusCode)
	}
}

// TestConsoleAssignRevokeRoleScreen проверяет экранное назначение и отзыв роли с
// возвратом на экран (PRG).
func TestConsoleAssignRevokeRoleScreen(t *testing.T) {
	server := newConsoleServer(t, nil)

	assign := postForm(t, server, "/console/users/roles/assign", url.Values{
		"tenant_id":  {"tenant-1"},
		"actor":      {"user-1"},
		"command_id": {"cmd-assign"},
		"user_id":    {"user-1"},
		"role":       {string(domain.RoleClient)},
	})
	if assign.StatusCode != http.StatusSeeOther {
		t.Fatalf("назначение роли = %d, ожидался 303", assign.StatusCode)
	}
	assign.Body.Close()

	page := body(t, get(t, server, "/console/users?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, string(domain.RoleClient)) {
		t.Fatalf("экран не показал назначенную роль:\n%s", page)
	}

	revoke := postForm(t, server, "/console/users/roles/revoke", url.Values{
		"tenant_id":  {"tenant-1"},
		"actor":      {"user-1"},
		"command_id": {"cmd-revoke"},
		"user_id":    {"user-1"},
		"role":       {string(domain.RoleClient)},
	})
	if revoke.StatusCode != http.StatusSeeOther {
		t.Fatalf("отзыв роли = %d, ожидался 303", revoke.StatusCode)
	}
	revoke.Body.Close()
}

// TestConsoleAuditImmutableAndExport проверяет, что журнал аудита показывается без
// средств правки, а выгрузка идёт CSV с запретом кэширования.
func TestConsoleAuditImmutableAndExport(t *testing.T) {
	server := newConsoleServer(t, nil)

	page := body(t, get(t, server, "/console/audit?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, string(domain.ActionTenantCreated)) {
		t.Fatalf("журнал не показал запись:\n%s", page)
	}
	if strings.Contains(page, "<form") {
		t.Fatalf("экран журнала содержит форму правки записи:\n%s", page)
	}

	resp := get(t, server, "/console/audit/export?tenant_id=tenant-1&actor=user-1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("выгрузка = %d, ожидался 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, ожидался no-store", got)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, ожидался text/csv", ct)
	}
	exported := body(t, resp)
	if !strings.Contains(exported, string(domain.ActionTenantCreated)) || !strings.Contains(exported, "tenant-1") {
		t.Fatalf("выгрузка не содержит ожидаемых полей:\n%s", exported)
	}
}

// TestConsoleIntegrationsUnavailable проверяет честный исход при неподключённом
// владельце адаптеров: явное сообщение, а не пустой успех.
func TestConsoleIntegrationsUnavailable(t *testing.T) {
	server := newConsoleServer(t, nil)
	page := body(t, get(t, server, "/console/integrations?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(page, "Состояние интеграций недоступно") {
		t.Fatalf("раздел не сообщил о недоступности:\n%s", page)
	}
}

// TestConsoleIntegrationsMasked проверяет, что произвольный текст причины не
// проходит в разметку, а объявленная причина показывается нормализованной.
func TestConsoleIntegrationsMasked(t *testing.T) {
	catalog := consoleFakeCatalog{states: []ports.AdapterState{
		{Name: "crawler", CapabilitySetVersion: "1.2.0", Health: ports.AdapterDown, Reason: ports.AdapterReason("token=supersecret")},
		{Name: "mail", CapabilitySetVersion: "1.0.0", Health: ports.AdapterDown, Reason: ports.AdapterReasonUnreachable},
	}}
	server := newConsoleServer(t, catalog)
	page := body(t, get(t, server, "/console/integrations?tenant_id=tenant-1&actor=user-1"))
	if strings.Contains(page, "supersecret") {
		t.Fatalf("секрет утёк в разметку:\n%s", page)
	}
	if !strings.Contains(page, "crawler") || !strings.Contains(page, "mail") {
		t.Fatalf("раздел не показал адаптеры:\n%s", page)
	}
	if !strings.Contains(page, "не отвечает") {
		t.Fatalf("объявленная причина не показана:\n%s", page)
	}
}
