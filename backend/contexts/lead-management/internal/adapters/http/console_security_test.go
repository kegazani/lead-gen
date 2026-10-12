package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestConsolePagesForbidCaching проверяет критерий задачи 7.5: страницы консоли
// несут данные лидов и не кэшируются.
func TestConsolePagesForbidCaching(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	for _, path := range []string{"/", "/cards", "/leads"} {
		resp := client.get(t, path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, ожидался 200", path, resp.StatusCode)
		}
		if cacheControl := resp.Header.Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
			t.Errorf("GET %s: Cache-Control = %q, ожидался no-store", path, cacheControl)
		}
	}
}

// TestConsoleLoginRateLimited проверяет критерий задачи 7.5: превышение порога
// входов блокируется кодом 429.
func TestConsoleLoginRateLimited(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)

	blocked := false
	for attempt := 0; attempt < 31; attempt++ {
		resp := client.do(t, http.MethodPost, "/login", url.Values{
			"username": {"operator"},
			"password": {"wrong"},
		})
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			blocked = true
			break
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("POST /login (%d) = %d, ожидался 401", attempt, resp.StatusCode)
		}
	}
	if !blocked {
		t.Fatalf("превышение порога входов не привело к 429")
	}
}

// TestConsoleAccessibilityMarkup проверяет критерий задачи 7.3: каркас подключает
// общий скрипт управления фокусом, имеет цель скип-ссылки и область живых
// объявлений; критерий 7.4: смысл статусов опирается на общее оформление.
func TestConsoleAccessibilityMarkup(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/")
	page := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, ожидался 200", resp.StatusCode)
	}
	for _, want := range []string{
		`/static/console-a11y.js`,
		`/static/console.css`,
		`<main class="page" id="main-content">`,
		`class="skip-link"`,
		`data-live-region`,
		`aria-live="polite"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("разметка главной не содержит %q:\n%s", want, page)
		}
	}

	// Подменяемый контейнер списка шаблонов несёт атрибуты возврата фокуса и
	// живого объявления (7.3).
	templates := readBody(t, client.get(t, "/templates"))
	for _, want := range []string{`data-focus-after-swap="#template-list"`, `data-live-announce=`} {
		if !strings.Contains(templates, want) {
			t.Errorf("разметка библиотеки шаблонов не содержит %q:\n%s", want, templates)
		}
	}
}
