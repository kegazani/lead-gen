package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestConsolePagesForbidCaching проверяет критерий задачи 7.5: экран очереди
// черновиков несёт персональные данные и не кэшируется.
func TestConsolePagesForbidCaching(t *testing.T) {
	fx := newFixture(t)
	session := loginConsole(t, fx)

	resp, err := session.client.Get(fx.server.URL + "/outreach/drafts?tenant_id=" + string(testTenant))
	if err != nil {
		t.Fatalf("GET /outreach/drafts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /outreach/drafts = %d, ожидался 200", resp.StatusCode)
	}
	if cacheControl := resp.Header.Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("Cache-Control = %q, ожидался no-store", cacheControl)
	}
}

// TestConsoleLoginRateLimited проверяет критерий задачи 7.5: превышение порога
// входов блокируется кодом 429.
func TestConsoleLoginRateLimited(t *testing.T) {
	fx := newFixture(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	blocked := false
	for attempt := 0; attempt < sensitiveLimit+1; attempt++ {
		resp, err := client.PostForm(fx.server.URL+"/login", url.Values{
			"username": {"operator-1"},
			"password": {"wrong"},
		})
		if err != nil {
			t.Fatalf("POST /login (%d): %v", attempt, err)
		}
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

// TestConsoleAccessibilityMarkup проверяет критерий задачи 7.3 на уровне разметки:
// каркас подключает общий скрипт, имеет цель скип-ссылки и область живых
// объявлений, а раздел «Рассылки» выделен как активный в общей навигации.
func TestConsoleAccessibilityMarkup(t *testing.T) {
	fx := newFixture(t)
	session := loginConsole(t, fx)
	code, page := session.getConsole(t, fx.server.URL+"/outreach/drafts?tenant_id="+string(testTenant))
	if code != http.StatusOK {
		t.Fatalf("GET /outreach/drafts = %d, ожидался 200", code)
	}
	for _, want := range []string{
		`/static/console-a11y.js`,
		`<main class="page" id="main-content">`,
		`class="skip-link"`,
		`data-live-region`,
		`aria-live="polite"`,
		`href="/outreach/drafts?actor=operator-1&amp;tenant_id=tenant-1" class="is-active" aria-current="page">Рассылки`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("разметка очереди не содержит %q:\n%s", want, page)
		}
	}
}
