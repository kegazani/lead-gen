package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReportScreensContainNoPersonalData проверяет срез 3 (задача 3.5): отчётные
// (агрегатные) экраны lead-management — дашборд лидов (главная) и воронка — не
// содержат полей, идентифицирующих физлицо: адреса электронной почты, телефона и
// роли контакта. Контакты в пуле лидов есть, но в агрегаты не попадают: отчёты
// строятся только по обезличенным данным (architecture.md §3.15, BRD §3.15).
func TestReportScreensContainNoPersonalData(t *testing.T) {
	const (
		companyName = "Пример Компании"
		email       = "person@example.com"
		phone       = "+7 999 123-45-67"
		role        = "основатель"
	)

	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", companyName, "Описание компании")
	seedContact(t, env.contacts, "example.com", email, phone, role)

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	for _, path := range []string{"/", "/funnel"} {
		resp := client.get(t, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, ожидался 200", path, resp.StatusCode)
		}
		html := readBody(t, resp)

		// Проверка не пустая: экран отрисовал агрегат по компании.
		if !strings.Contains(html, companyName) {
			t.Fatalf("отчётный экран %s не показал компанию %q:\n%s", path, companyName, html)
		}
		for _, field := range []string{email, phone, role} {
			if strings.Contains(html, field) {
				t.Fatalf("отчётный экран %s содержит персональные данные %q:\n%s", path, field, html)
			}
		}
	}
}
