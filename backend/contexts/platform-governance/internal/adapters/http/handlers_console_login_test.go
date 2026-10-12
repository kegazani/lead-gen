package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/platform/consoleauth"
)

// newConsoleAuthServer собирает срез с включённым входом консоли платформы.
func newConsoleAuthServer(t *testing.T) *httptest.Server {
	return newConsoleAuthServerWithPassword(t, "admin")
}

func newConsoleAuthServerWithPassword(t *testing.T, password string) *httptest.Server {
	t.Helper()
	adminGrants, err := grants.FromStrings("admin", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{
		Grants:      adminGrants,
		Requests:    newFakeRequests(),
		Operators:   []string{"admin"},
		ConsoleAuth: consoleauth.Config{Username: "admin", Password: password},
		Clock:       fixedClock{at: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		IDs:         &seqIDs{},
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// TestConsoleAuthGuard проверяет: без сессии консоль перенаправляет на вход,
// вход выдаёт сессию и открывает раздел, клиентский портал не затронут.
func TestConsoleAuthGuard(t *testing.T) {
	server := newConsoleAuthServer(t)

	// Без сессии — редирект на вход консоли.
	guard := getCookie(t, server, "/console", nil)
	location := guard.Header.Get("Location")
	guard.Body.Close()
	if guard.StatusCode != http.StatusSeeOther || location != "/console/login" {
		t.Fatalf("GET /console без сессии = %d, Location = %q, ожидался 303 на /console/login", guard.StatusCode, location)
	}

	// Страница входа доступна без сессии.
	form := getCookie(t, server, "/console/login", nil)
	if form.StatusCode != http.StatusOK {
		form.Body.Close()
		t.Fatalf("GET /console/login = %d, ожидался 200", form.StatusCode)
	}
	form.Body.Close()

	// Вход выдаёт сессию и ведёт в раздел регистраций.
	login := postForm(t, server, "/console/login", url.Values{"username": {"admin"}, "password": {"admin"}})
	loginLocation := login.Header.Get("Location")
	var session *http.Cookie
	for _, cookie := range login.Cookies() {
		if cookie.Name == consoleauth.CookieName {
			session = cookie
		}
	}
	login.Body.Close()
	if login.StatusCode != http.StatusSeeOther || loginLocation != "/console/registrations" {
		t.Fatalf("POST /console/login = %d, Location = %q", login.StatusCode, loginLocation)
	}
	if session == nil {
		t.Fatalf("сессия консоли (cookie %s) не установлена", consoleauth.CookieName)
	}

	// С сессией раздел открывается, субъект берётся из сессии.
	page := body(t, getCookie(t, server, "/console/registrations", session))
	if !strings.Contains(page, "Заявки на регистрацию") {
		t.Fatalf("раздел регистраций не открылся с сессией:\n%s", page)
	}

	// Клиентский портал не затронут: вход доступен анонимно.
	portal := getCookie(t, server, "/login", nil)
	portal.Body.Close()
	if portal.StatusCode != http.StatusOK {
		t.Fatalf("GET /login (портал) = %d, ожидался 200", portal.StatusCode)
	}

	// Неверные данные — 401 и без сессии.
	bad := postForm(t, server, "/console/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("неверный вход = %d, ожидался 401", bad.StatusCode)
	}

	empty := postForm(t, server, "/console/login", url.Values{"username": {"admin"}, "password": {""}})
	defer empty.Body.Close()
	if empty.StatusCode != http.StatusUnauthorized || len(empty.Cookies()) != 0 {
		t.Fatalf("пустой пароль создал/принял вход: status=%d cookies=%v", empty.StatusCode, empty.Cookies())
	}
}

func TestConsoleAuthAcceptsWhitespacePasswordVerbatim(t *testing.T) {
	const password = "  "
	server := newConsoleAuthServerWithPassword(t, password)

	login := postForm(t, server, "/console/login", url.Values{"username": {"admin"}, "password": {password}})
	defer login.Body.Close()
	if login.StatusCode != http.StatusSeeOther || login.Header.Get("Location") != "/console/registrations" {
		t.Fatalf("вход с паролем-пробелами: status=%d location=%q", login.StatusCode, login.Header.Get("Location"))
	}
	for _, cookie := range login.Cookies() {
		if cookie.Name == consoleauth.CookieName && cookie.Value != "" {
			return
		}
	}
	t.Fatal("вход с паролем-пробелами не создал сессию")
}
