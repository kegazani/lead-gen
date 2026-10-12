package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// stageChangeEvent выполняет перенос лида по форме воронки и возвращает последнее
// опубликованное событие дашборда. tenant — значение tenant_id; inQuery — где оно
// передано (строка запроса или тело формы); пустой tenant — поле не передаётся.
func stageChangeEvent(t *testing.T, tenant string, inQuery bool) ports.AnalyticsEvent {
	t.Helper()
	env := newTestEnv()
	seedCompanyInTenant(t, env.profiles, effectiveTenant(tenant), "example.com", "Пример Компании", "Описание компании")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrfPage := "/funnel"
	if tenant != "" {
		csrfPage += "?tenant_id=" + url.QueryEscape(tenant)
	}
	csrf := client.csrfForPage(t, csrfPage)

	path := "/funnel/example.com/stage"
	form := url.Values{"stage": {"warming"}, "csrf": {csrf}}
	if tenant != "" {
		if inQuery {
			path += "?tenant_id=" + url.QueryEscape(tenant)
		} else {
			form.Set("tenant_id", tenant)
		}
	}
	resp := client.postForm(t, path, form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST %s = %d, ожидался 303", path, resp.StatusCode)
	}
	resp.Body.Close()
	return lastEvent(t, env.events)
}

// sendEvent выполняет отправку письма лиду по форме карточки и возвращает
// последнее опубликованное событие дашборда. tenant — значение tenant_id в теле
// формы (пусто — поле не передаётся).
func sendEvent(t *testing.T, tenant string) ports.AnalyticsEvent {
	t.Helper()
	env := newTestEnv()
	seedCompanyInTenant(t, env.profiles, effectiveTenant(tenant), "example.com", "Пример Компании", "Описание компании")
	seedContact(t, env.contacts, "example.com", "hello@example.com", "+7 999 123-45-67", "основатель")
	seedTemplate(t, env.templates, "intro", "Приветствие", "Тема для {{company}}", "Здравствуйте, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
	)

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrfPage := "/cards/example.com"
	if tenant != "" {
		csrfPage += "?tenant_id=" + url.QueryEscape(tenant)
	}
	csrf := client.csrfForPage(t, csrfPage)

	form := url.Values{"template": {"intro"}, "key": {"send-key-1"}, "csrf": {csrf}}
	if tenant != "" {
		form.Set("tenant_id", tenant)
	}
	resp := client.postForm(t, "/cards/example.com/send", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /cards/example.com/send = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()
	return lastEvent(t, env.events)
}

// effectiveTenant — активный тенант запроса: явный tenant_id или тенант
// конфигурации тестового сервера. Карточка сидируется в нём же: хранилище
// скоупится активным тенантом.
func effectiveTenant(tenant string) string {
	if tenant == "" {
		return testTenantID
	}
	return tenant
}

// TestLeadConsoleScopesCardsByTenant проверяет изоляцию витрины лидов по активному
// тенанту: ссылка консоли несёт tenant_id, и хранилище возвращает только карточки
// этого тенанта, а чтение карточки чужого тенанта не раскрывает данные.
func TestLeadConsoleScopesCardsByTenant(t *testing.T) {
	env := newTestEnv()
	seedCompanyInTenant(t, env.profiles, "tenant-a", "a.example", "Компания A", "Описание A")
	seedCompanyInTenant(t, env.profiles, "tenant-b", "b.example", "Компания B", "Описание B")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	pageA := readBody(t, client.get(t, "/cards?tenant_id=tenant-a"))
	if !strings.Contains(pageA, "Компания A") || strings.Contains(pageA, "Компания B") {
		t.Fatalf("витрина тенанта A пересекает границу тенанта:\n%s", pageA)
	}
	pageB := readBody(t, client.get(t, "/cards?tenant_id=tenant-b"))
	if !strings.Contains(pageB, "Компания B") || strings.Contains(pageB, "Компания A") {
		t.Fatalf("витрина тенанта B пересекает границу тенанта:\n%s", pageB)
	}

	foreign := client.get(t, "/cards/b.example?tenant_id=tenant-a")
	foreign.Body.Close()
	if foreign.StatusCode != http.StatusNotFound {
		t.Fatalf("чтение карточки чужого тенанта = %d, ожидался 404", foreign.StatusCode)
	}
}

// lastEvent извлекает последнее опубликованное событие дашборда.
func lastEvent(t *testing.T, publisher *recordingPublisher) ports.AnalyticsEvent {
	t.Helper()
	event, ok := publisher.last()
	if !ok {
		t.Fatal("событие дашборда не опубликовано")
	}
	return event
}

// TestActiveTenantFromRequestChangesSelection проверяет единый способ определения
// активного тенанта: tenant_id в запросе консоли лидов (строка запроса или форма)
// задаёт тенант команды, и событие уходит в срез выбранного тенанта, а не в тенант
// конфигурации.
func TestActiveTenantFromRequestChangesSelection(t *testing.T) {
	const active = "tenant-client"

	cases := []struct {
		name    string
		inQuery bool
	}{
		{"tenant_id в строке запроса", true},
		{"tenant_id в форме", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stage := stageChangeEvent(t, active, tc.inQuery)
			if stage.Kind != ports.AnalyticsKindLeadWorkflow {
				t.Fatalf("вид события смены стадии = %q, ожидался %q", stage.Kind, ports.AnalyticsKindLeadWorkflow)
			}
			if stage.TenantID != active {
				t.Fatalf("тенант события смены стадии = %q, ожидался %q", stage.TenantID, active)
			}
		})
	}

	sent := sendEvent(t, active)
	if sent.Kind != ports.AnalyticsKindMessageSent {
		t.Fatalf("вид события отправки = %q, ожидался %q", sent.Kind, ports.AnalyticsKindMessageSent)
	}
	if sent.TenantID != active {
		t.Fatalf("тенант события отправки = %q, ожидался %q", sent.TenantID, active)
	}
}

// TestActiveTenantEmptyFallsBackToConfigured проверяет прежнее поведение: пустой
// tenant_id оставляет активным тенант конфигурации процесса.
func TestActiveTenantEmptyFallsBackToConfigured(t *testing.T) {
	stage := stageChangeEvent(t, "", false)
	if stage.TenantID != testTenantID {
		t.Fatalf("тенант события смены стадии = %q, ожидался фолбэк %q", stage.TenantID, testTenantID)
	}

	sent := sendEvent(t, "")
	if sent.TenantID != testTenantID {
		t.Fatalf("тенант события отправки = %q, ожидался фолбэк %q", sent.TenantID, testTenantID)
	}
}

// TestLeadConsoleUsesSelectedTenantCookie проверяет, что без tenant_id в адресе
// консоль берёт выбранный оператором тенант из cookie консоли: оператор,
// переключивший тенант на экране выбора, видит его данные напрямую.
func TestLeadConsoleUsesSelectedTenantCookie(t *testing.T) {
	env := newTestEnv()
	seedCompanyInTenant(t, env.profiles, "tenant-selected", "sel.example", "Компания Selected", "Описание")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/cards", nil)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: client.session})
	req.AddCookie(&http.Cookie{Name: "active_tenant", Value: "tenant-selected"})
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET /cards: %v", err)
	}
	page := readBody(t, resp)
	if !strings.Contains(page, "Компания Selected") {
		t.Fatalf("консоль не взяла выбранный тенант из cookie:\n%s", page)
	}
}

// Новая таблица показывает отсутствие email и не теряет tenant при открытии карточки.
func TestSavedLeadsWithoutEmailKeepTenantScope(t *testing.T) {
	env := newTestEnv()
	seedCompanyInTenant(t, env.profiles, "tenant-a", "own.example", "Своя компания", "Описание")
	seedCompanyInTenant(t, env.profiles, "tenant-b", "foreign.example", "Чужая компания", "Описание")
	server := httptest.NewServer(env.server)
	defer server.Close()
	client := newConsoleClient(server)
	client.login(t, "operator", "secret")
	for _, path := range []string{"/leads?tenant_id=tenant-a", "/cards?tenant_id=tenant-a"} {
		html := readBody(t, client.get(t, path))
		if !strings.Contains(html, "Своя компания") || strings.Contains(html, "Чужая компания") {
			t.Fatalf("tenant-изоляция %s", path)
		}
		if !strings.Contains(html, `data-tenant-id="tenant-a"`) {
			t.Fatal("обновление списка потеряло tenant")
		}
	}
	html := readBody(t, client.get(t, "/leads?tenant_id=tenant-a"))
	if !strings.Contains(html, `href="/cards/own.example?actor=operator&amp;tenant_id=tenant-a"`) || !strings.Contains(html, "нет email") {
		t.Fatalf("карточка без email не видна или ссылка потеряла tenant: %s", html)
	}
	response := client.get(t, "/cards/own.example?tenant_id=tenant-a")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("карточка не открылась: %d", response.StatusCode)
	}
}
