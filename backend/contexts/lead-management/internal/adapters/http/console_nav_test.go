package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConsoleNavOpensLeadsDashboardAndJournal проверяет срез 3 (задача 3.1):
// главная — дашборд лидов — и журнал заданий lead-management открываются из
// общей навигации консоли. Вход один: разделы «Заявки», «Исполнение», «Лиды» и
// «Отчёты» доступны с той же сессией, а раздел текущего сервиса («Лиды») остаётся
// относительным, тогда как разделы других сервисов ведут по их базовым адресам.
// Ссылки несут контекст активного тенанта и оператора (задача 3.2): выбранный
// тенант не теряется при переходе между консолями разных сервисов.
func TestConsoleNavOpensLeadsDashboardAndJournal(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	// Главная содержит общую навигацию: активный раздел «Лиды» и переходы в
	// разделы соседних сервисов консоли.
	home := client.get(t, "/")
	if home.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, ожидался 200", home.StatusCode)
	}
	html := readBody(t, home)
	for _, want := range []string{
		`href="https://requests.example.test/validation?actor=operator&amp;tenant_id=tenant-config">Заявки`,
		`href="https://requests.example.test/approvals?actor=operator&amp;tenant_id=tenant-config">Исполнение`,
		`href="/?actor=operator&amp;tenant_id=tenant-config" class="is-active" aria-current="page">Лиды`,
		`href="https://reports.example.test/dashboard?actor=operator&amp;tenant_id=tenant-config">Отчёты`,
		"Журнал заданий",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("главная не открывает раздел общей навигации %q:\n%s", want, html)
		}
	}

	// Журнал заданий открывается по своему адресу как живой фрагмент консоли.
	journal := client.get(t, "/jobs")
	if journal.StatusCode != http.StatusOK {
		t.Fatalf("GET /jobs = %d, ожидался 200", journal.StatusCode)
	}
	body := readBody(t, journal)
	if !strings.Contains(body, `class="journal-list"`) {
		t.Fatalf("журнал заданий не отдал список заданий:\n%s", body)
	}
	if !strings.Contains(body, "Заданий пока нет") {
		t.Fatalf("журнал заданий не показал пустое состояние:\n%s", body)
	}
}
