package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestConsolePagesForbidCaching проверяет критерий задачи 7.5: ответы экранов с
// операционными данными не кэшируются — браузер и посредник не должны хранить
// персональные данные.
func TestConsolePagesForbidCaching(t *testing.T) {
	server := newServer(t, day(1))
	for _, path := range []string{"/dashboard?tenant_id=t1", "/reports?tenant_id=t1"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, ожидался 200", path, resp.StatusCode)
		}
		if cacheControl := resp.Header.Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
			t.Errorf("GET %s: Cache-Control = %q, ожидался no-store", path, cacheControl)
		}
	}
}

// TestConsoleExportRateLimited проверяет критерий задачи 7.5: постановка дорогой
// выгрузки ограничена по частоте — превышение порога даёт 429.
func TestConsoleExportRateLimited(t *testing.T) {
	server := newServer(t, day(1))
	// Порог чувствительного действия консоли: 30 запросов в минуту на клиента.
	const limit = 30
	blocked := false
	for attempt := 0; attempt < limit+1; attempt++ {
		resp, err := http.Post(server.URL+"/reports/exports?tenant_id=t1&format=csv", "application/json", nil)
		if err != nil {
			t.Fatalf("POST /reports/exports (%d): %v", attempt, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			blocked = true
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter == "" {
				t.Errorf("429 без заголовка Retry-After")
			}
			break
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST /reports/exports (%d) = %d, ожидался 202", attempt, resp.StatusCode)
		}
	}
	if !blocked {
		t.Fatalf("превышение порога не привело к 429")
	}
}

// TestConsoleAccessibilityMarkup проверяет критерий задачи 7.3 на уровне разметки:
// каркас подключает общий скрипт управления фокусом, имеет цель скип-ссылки и
// область живых объявлений.
func TestConsoleAccessibilityMarkup(t *testing.T) {
	server := newServer(t, day(1))
	status, page := getPage(t, server, "/dashboard?tenant_id=t1")
	if status != http.StatusOK {
		t.Fatalf("GET /dashboard = %d, ожидался 200", status)
	}
	for _, want := range []string{
		`/static/console-a11y.js`,
		`<main class="page" id="main-content">`,
		`class="skip-link"`,
		`data-live-region`,
		`aria-live="polite"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("разметка дашборда не содержит %q:\n%s", want, page)
		}
	}
}
