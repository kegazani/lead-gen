package httpapi_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"lead-gen/backend/platform/consolescope"
)

// TestConsoleNavUnified проверяет, что навигация платформенной консоли объединяет
// операторские и платформенные разделы.
func TestConsoleNavUnified(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	registerClientPending(t, slice, "nav@example.ru", "7700000001", "nav-co@example.ru")

	page := body(t, get(t, server, "/console/registrations?actor=admin-1"))
	for _, want := range []string{"Заявки", "Исполнение", "Лиды", "Отчёты", "Рассылки", "Тенант", "Пользователи", "Регистрации", "Интеграции", "Аудит", "Топология", "Операции"} {
		if !strings.Contains(page, ">"+want+"<") {
			t.Fatalf("навигация не содержит раздел %q:\n%s", want, page)
		}
	}
}

// TestConsoleUsersRedirectsToTenantSelector проверяет, что раздел, требующий
// тенанта, без него ведёт к выбору тенанта, а не отказывает.
func TestConsoleUsersRedirectsToTenantSelector(t *testing.T) {
	_, server := newConsoleOperatorServer(t)
	resp := getCookie(t, server, "/console/users?actor=admin-1", nil)
	location := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || location != "/console/tenant?actor=admin-1" {
		t.Fatalf("GET /console/users без тенанта = %d, Location = %q", resp.StatusCode, location)
	}
}

// TestConsoleUsersUsesActiveTenantCookie проверяет, что тенант берётся из cookie
// консоли: раздел открывается без параметра tenant_id.
func TestConsoleUsersUsesActiveTenantCookie(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "cookie@example.ru", "7700000002", "cookie-co@example.ru")
	approve := postForm(t, server, "/console/registrations/"+tenantID+"/approve", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-nav"},
	})
	approve.Body.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/console/users?actor=admin-1", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: consolescope.CookieName, Value: tenantID})
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET /console/users: %v", err)
	}
	page := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /console/users с cookie тенанта = %d, ожидался 200:\n%s", resp.StatusCode, page)
	}
	if !strings.Contains(page, tenantID) {
		t.Fatalf("раздел не использует тенант из cookie:\n%s", page)
	}
}
