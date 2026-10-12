package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	analytics "lead-gen/backend/contexts/analytics"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/consolescope"
	"lead-gen/frontend/components"
)

// Общий вход принимается отчётами, а выбранная компания и субъект не теряются.
func TestReportsAcceptSharedConsoleSession(t *testing.T) {
	store := consoleauth.NewMemoryStore()
	auth := consoleauth.Config{Username: "operator", Password: "secret", Store: store}
	platformLogin := consoleauth.New(auth)
	id, err := platformLogin.Login("operator", "secret")
	if err != nil {
		t.Fatal(err)
	}
	slice, err := analytics.Build(analytics.Config{
		ConsoleAuth: auth, BasePath: "/reporting",
		Console:          components.ConsoleBases{components.ConsoleServicePlatform: "/admin"},
		ExternalPrefixes: []string{"/admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(slice.Server)
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path string, form url.Values, session bool) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if session {
			req.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: id})
			req.AddCookie(&http.Cookie{Name: consolescope.CookieName, Value: "selected-company"})
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for _, path := range []string{"/reporting/dashboard", "/reporting/reports"} {
		anonymous := request(http.MethodGet, path, nil, false)
		anonymous.Body.Close()
		if anonymous.StatusCode != http.StatusSeeOther || anonymous.Header.Get("Location") != "/admin/console/login" {
			t.Fatalf("анонимный вход %s: %d %q", path, anonymous.StatusCode, anonymous.Header.Get("Location"))
		}
		resp := request(http.MethodGet, path+"?actor=stranger", nil, true)
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		page := string(data)
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, "selected-company") || !strings.Contains(page, "/admin/console/users?actor=operator&amp;tenant_id=selected-company") {
			t.Fatalf("отчёты потеряли общую сессию, компанию или субъекта: %d %s", resp.StatusCode, page)
		}
	}
	// HTML-экспорт сохраняет защиту CSRF общего входа.
	bad := request(http.MethodPost, "/reporting/reports/exports/html?format=csv", nil, true)
	bad.Body.Close()
	if bad.StatusCode != http.StatusForbidden {
		t.Fatalf("экспорт без CSRF: %d", bad.StatusCode)
	}
	session, ok := platformLogin.Authenticate(id)
	if !ok {
		t.Fatal("сессия потеряна")
	}
	good := request(http.MethodPost, "/reporting/reports/exports/html?format=csv", url.Values{"csrf": {session.CSRF()}}, true)
	good.Body.Close()
	if good.StatusCode != http.StatusSeeOther {
		t.Fatalf("экспорт с общим CSRF: %d", good.StatusCode)
	}
	platformLogin.Logout(id)
	expired := request(http.MethodGet, "/reporting/dashboard", nil, true)
	expired.Body.Close()
	if expired.StatusCode != http.StatusSeeOther {
		t.Fatal("отчёты приняли удалённую сессию")
	}
}
