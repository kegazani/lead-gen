package httpapi_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestConsolePagesForbidCaching проверяет критерий задачи 7.5: экраны заявок
// содержат данные заявителя и не кэшируются.
func TestConsolePagesForbidCaching(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(7)}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	resp := client.get(t, "/approvals?tenant_id="+tenantID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /approvals = %d, ожидался 200", resp.StatusCode)
	}
	if cacheControl := resp.Header.Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("Cache-Control = %q, ожидался no-store", cacheControl)
	}
}

// TestConsoleLoginRateLimited проверяет критерий задачи 7.5: превышение порога
// входов блокируется кодом 429.
func TestConsoleLoginRateLimited(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{}, stubWorkflow{})
	client := newConsoleClient(server)

	blocked := false
	for attempt := 0; attempt < 31; attempt++ {
		resp := client.do(t, http.MethodPost, "/login", url.Values{
			"username": {consoleUser},
			"password": {"wrong"},
		}, false)
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
// каркас подключает общий скрипт управления фокусом, имеет цель скип-ссылки и
// область живых объявлений; подменяемые контейнеры несут атрибуты возврата фокуса.
func TestConsoleAccessibilityMarkup(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(7)}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	resp := client.get(t, "/requests/"+requestID+"/approval?tenant_id="+tenantID)
	page := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET экрана утверждения = %d, ожидался 200", resp.StatusCode)
	}
	for _, want := range []string{
		`/static/console-a11y.js`,
		`<main class="page" id="main-content">`,
		`class="skip-link"`,
		`data-live-region`,
		`aria-live="polite"`,
		`data-focus-after-swap="#workflow"`,
		`data-live-announce=`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("разметка экрана не содержит %q:\n%s", want, page)
		}
	}
}
