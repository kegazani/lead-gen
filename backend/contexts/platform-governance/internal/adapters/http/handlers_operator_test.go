package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/platform/consolescope"
)

// newOperatorSlice собирает срез с платформенным оператором operator-1, который не
// состоит пользователем ни в одном тенанте: тенанты создаёт администратор admin-1
// (у него есть грант), а оператор задан конфигурацией.
func newOperatorSlice(t *testing.T) *platformgovernance.Slice {
	t.Helper()
	grantsTable, err := grants.New(map[string][]string{
		"admin-1": {string(domain.RoleAdmin)},
	})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{
		Grants:    grantsTable,
		Operators: []string{"operator-1"},
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	createTenantForConsole(t, slice, "cmd-1", "tenant-1", "ООО Ромашка", "user-1", []string{string(domain.RoleAdmin)})
	createTenantForConsole(t, slice, "cmd-2", "tenant-2", "ООО Василёк", "user-3", []string{string(domain.RoleClient)})
	return slice
}

// TestOperatorSeesTenantListWithoutActiveTenant проверяет, что платформенный
// оператор открывает список тенантов без активного тенанта: право даёт
// принадлежность к операторам, а не роль в конкретном тенанте.
func TestOperatorSeesTenantListWithoutActiveTenant(t *testing.T) {
	server := httptest.NewServer(newOperatorSlice(t).Server)
	defer server.Close()

	resp := get(t, server, "/console/tenant?actor=operator-1")
	page := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список тенантов для оператора = %d, ожидался 200:\n%s", resp.StatusCode, page)
	}
	if !strings.Contains(page, "ООО Ромашка") || !strings.Contains(page, "ООО Василёк") {
		t.Fatalf("список тенантов не показал все тенанты оператору:\n%s", page)
	}
}

// TestOperatorActsInForeignTenant проверяет, что оператор действует в тенанте, где
// он не состоит пользователем: решение о доступе выдаёт роль администратора
// платформы.
func TestOperatorActsInForeignTenant(t *testing.T) {
	server := httptest.NewServer(newOperatorSlice(t).Server)
	defer server.Close()

	page := body(t, get(t, server, "/console/users?tenant_id=tenant-2&actor=operator-1"))
	if !strings.Contains(page, "login-user-3") {
		t.Fatalf("оператор не увидел состав чужого тенанта:\n%s", page)
	}
}

// TestNonOperatorWithoutTenantDenied проверяет, что не-оператор без активного
// тенанта не открывает список тенантов: отказ на сервере.
func TestNonOperatorWithoutTenantDenied(t *testing.T) {
	server := httptest.NewServer(newOperatorSlice(t).Server)
	defer server.Close()

	resp := get(t, server, "/console/tenant?actor=stranger")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("не-оператор без тенанта = %d, ожидался 400", resp.StatusCode)
	}
}

// TestOperatorSwitchRecordsConsoleScope проверяет, что переключение тенанта
// оператором фиксирует выбор в cookie консоли: остальные консоли читают его как
// фолбэк без параметра tenant_id.
func TestOperatorSwitchRecordsConsoleScope(t *testing.T) {
	server := httptest.NewServer(newOperatorSlice(t).Server)
	defer server.Close()

	resp := postForm(t, server, "/console/tenant/switch", url.Values{
		"tenant_id": {"tenant-2"},
		"actor":     {"operator-1"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("переключение оператором = %d, ожидался 303", resp.StatusCode)
	}
	var scope *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == consolescope.CookieName {
			scope = cookie
		}
	}
	if scope == nil || scope.Value != "tenant-2" {
		t.Fatalf("выбор тенанта не записан в cookie консоли: %+v", resp.Cookies())
	}
}
