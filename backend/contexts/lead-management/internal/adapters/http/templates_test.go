package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// templateIDFromLocation извлекает идентификатор шаблона из адреса правки
// /templates/{id}/edit.
func templateIDFromLocation(t *testing.T, location string) string {
	t.Helper()
	const prefix = "/templates/"
	const suffix = "/edit"
	if !strings.HasPrefix(location, prefix) || !strings.HasSuffix(location, suffix) {
		t.Fatalf("неожиданный Location после создания шаблона: %q", location)
	}
	return strings.TrimSuffix(strings.TrimPrefix(location, prefix), suffix)
}

// getWithHeaders выполняет GET с cookie сессии и дополнительными заголовками.
func (c *consoleClient) getWithHeaders(t *testing.T, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: c.session})
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// TestTemplatesCreateEditDeleteHTTP проверяет создание, правку и удаление шаблона
// через HTTP-интерфейс библиотеки.
func TestTemplatesCreateEditDeleteHTTP(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	csrf := client.csrfForPage(t, "/templates")
	create := client.postForm(t, "/templates", url.Values{
		"name":             {"Приветствие"},
		"subject":          {"Тема для {{company}}"},
		"body":             {"Здравствуйте, {{company}}!"},
		"var_company":      {"yes"},
		"fallback_company": {"ваша компания"},
		"csrf":             {csrf},
	})
	if create.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /templates = %d, ожидался 303", create.StatusCode)
	}
	id := templateIDFromLocation(t, create.Header.Get("Location"))
	create.Body.Close()

	list := client.get(t, "/templates")
	if html := readBody(t, list); !strings.Contains(html, "Приветствие") {
		t.Fatalf("список шаблонов не содержит созданный шаблон:\n%s", html)
	}

	editCSRF := client.csrfForPage(t, "/templates/"+url.PathEscape(id)+"/edit")
	edit := client.postForm(t, "/templates/"+url.PathEscape(id), url.Values{
		"name":             {"Приветствие v2"},
		"subject":          {"Новая тема для {{company}}"},
		"body":             {"Здравствуйте снова, {{company}}!"},
		"var_company":      {"yes"},
		"fallback_company": {"компания"},
		"csrf":             {editCSRF},
	})
	if edit.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /templates/%s = %d, ожидался 303", id, edit.StatusCode)
	}
	edit.Body.Close()

	stored, err := env.templates.FindByID(context.Background(), domain.TemplateID(id))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.Revision() != 2 {
		t.Fatalf("ревизия после правки = %d, ожидалась 2", stored.Revision())
	}

	deleteCSRF := client.csrfForPage(t, "/templates/"+url.PathEscape(id)+"/edit")
	deleted := client.postForm(t, "/templates/"+url.PathEscape(id)+"/delete", url.Values{"csrf": {deleteCSRF}})
	if deleted.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /templates/%s/delete = %d, ожидался 303", id, deleted.StatusCode)
	}
	deleted.Body.Close()
	if _, err := env.templates.FindByID(context.Background(), domain.TemplateID(id)); err == nil {
		t.Fatal("шаблон не удалён")
	}
}

// TestTemplatesSearchFragmentAndFullPage проверяет, что поиск по шаблонам отдаёт
// HTMX-фрагмент по запросу с заголовком и полную страницу без него.
func TestTemplatesSearchFragmentAndFullPage(t *testing.T) {
	env := newTestEnv()
	seedTemplate(t, env.templates, "alpha", "Альфа", "Тема A", "Тело A")
	seedTemplate(t, env.templates, "beta", "Бета", "Тема B", "Тело B")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	full := client.get(t, "/templates?q=бета")
	fullHTML := readBody(t, full)
	if !strings.Contains(fullHTML, "<!DOCTYPE") {
		t.Fatalf("прямой запрос должен отдавать полную страницу:\n%s", fullHTML)
	}
	if !strings.Contains(fullHTML, "Бета") || strings.Contains(fullHTML, "Альфа") {
		t.Fatalf("поиск не отфильтровал список:\n%s", fullHTML)
	}

	fragment := client.getWithHeaders(t, "/templates?q=бета", map[string]string{"HX-Request": "true"})
	fragmentHTML := readBody(t, fragment)
	if strings.Contains(fragmentHTML, "<!DOCTYPE") {
		t.Fatalf("HTMX-запрос должен отдавать фрагмент без каркаса страницы:\n%s", fragmentHTML)
	}
	if !strings.Contains(fragmentHTML, "Бета") || strings.Contains(fragmentHTML, "Альфа") {
		t.Fatalf("фрагмент не отфильтрован:\n%s", fragmentHTML)
	}
}

// TestTemplatesRejectUndeclaredPlaceholderViaServer проверяет серверную проверку:
// прямой POST с необъявленным плейсхолдером отклоняется независимо от браузера.
func TestTemplatesRejectUndeclaredPlaceholderViaServer(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	csrf := client.csrfForPage(t, "/templates")
	resp := client.postForm(t, "/templates", url.Values{
		"name":             {"Плохой"},
		"subject":          {"Тема"},
		"body":             {"Привет, {{company}} и {{unknown}}!"},
		"var_company":      {"yes"},
		"fallback_company": {"ваша компания"},
		"csrf":             {csrf},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST с необъявленным плейсхолдером = %d, ожидался 400", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "unknown") {
		t.Fatalf("ошибка не называет необъявленный плейсхолдер:\n%s", html)
	}
}

// TestCardSendPreviewShowsComposedEmail проверяет предпросмотр собранного письма с
// подставленными значениями и fallback.
func TestCardSendPreviewShowsComposedEmail(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "ООО Ромашка", "Описание")
	seedContact(t, env.contacts, "example.com", "anna@example.com", "+7 999 000-00-00", "CEO")
	seedTemplate(t, env.templates, "intro", "Приветствие", "Тема для {{company}}",
		"Здравствуйте, {{company}}! Отрасль: {{industry}}.",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
		domain.MergeVariable{Key: domain.MergeVariableIndustry, Fallback: "ваша отрасль"},
	)

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/cards/example.com?template=intro")
	html := readBody(t, resp)
	for _, want := range []string{"Тема для ООО Ромашка", "Здравствуйте, ООО Ромашка! Отрасль: ваша отрасль."} {
		if !strings.Contains(html, want) {
			t.Fatalf("предпросмотр не содержит %q:\n%s", want, html)
		}
	}

	// HTMX-предпросмотр отдаёт фрагмент собранного письма.
	preview := client.postForm(t, "/cards/example.com/send/preview", url.Values{
		"template": {"intro"},
		"csrf":     {client.csrfForPage(t, "/cards/example.com")},
	})
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("POST /cards/example.com/send/preview = %d", preview.StatusCode)
	}
	previewHTML := readBody(t, preview)
	if !strings.Contains(previewHTML, "Тема для ООО Ромашка") {
		t.Fatalf("фрагмент предпросмотра пуст:\n%s", previewHTML)
	}
}

// TestTemplateLibraryEndToEnd проверяет сквозной сценарий: создать шаблон → найти
// поиском при отправке → предпросмотреть → отправить → убедиться, что
// outbound-message зафиксировал ссылку на редакцию и собранное тело.
func TestTemplateLibraryEndToEnd(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "ООО Ромашка", "Описание")
	seedContact(t, env.contacts, "example.com", "anna@example.com", "+7 999 000-00-00", "CEO")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	csrf := client.csrfForPage(t, "/templates")
	create := client.postForm(t, "/templates", url.Values{
		"name":             {"Письмо клиенту"},
		"subject":          {"Здравствуйте, {{company}}"},
		"body":             {"Пишем вам, {{contact}} из {{company}}."},
		"var_company":      {"yes"},
		"fallback_company": {"ваша компания"},
		"var_contact":      {"yes"},
		"fallback_contact": {"коллега"},
		"csrf":             {csrf},
	})
	if create.StatusCode != http.StatusSeeOther {
		t.Fatalf("создание шаблона = %d, ожидался 303", create.StatusCode)
	}
	id := templateIDFromLocation(t, create.Header.Get("Location"))
	create.Body.Close()

	// Поиск при отправке по сегменту.
	send := client.get(t, "/send?q=клиенту")
	sendHTML := readBody(t, send)
	if !strings.Contains(sendHTML, "Письмо клиенту") {
		t.Fatalf("поиск при отправке не нашёл шаблон:\n%s", sendHTML)
	}

	// Предпросмотр для лида.
	preview := client.get(t, "/cards/example.com?template="+url.QueryEscape(id))
	previewHTML := readBody(t, preview)
	if !strings.Contains(previewHTML, "Пишем вам, CEO из ООО Ромашка.") {
		t.Fatalf("предпросмотр не собрал письмо:\n%s", previewHTML)
	}

	// Отправка.
	cardCSRF := client.csrfForPage(t, "/cards/example.com")
	resp := client.postForm(t, "/cards/example.com/send", url.Values{
		"template": {id},
		"key":      {"send-key-e2e"},
		"csrf":     {cardCSRF},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("отправка = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	if len(env.mail.sent) != 1 {
		t.Fatalf("письмо не доставлено: sent = %d", len(env.mail.sent))
	}
	if env.mail.sent[0].Subject != "Здравствуйте, ООО Ромашка" {
		t.Fatalf("тема письма = %q", env.mail.sent[0].Subject)
	}
	if env.mail.sent[0].Body != "Пишем вам, CEO из ООО Ромашка." {
		t.Fatalf("тело письма = %q", env.mail.sent[0].Body)
	}

	history, err := env.messages.ListByLead(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ListByLead: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("история = %d сообщений", len(history))
	}
	if want := id + "@1"; history[0].Template() != want {
		t.Fatalf("ссылка на шаблон = %q, ожидалась %q", history[0].Template(), want)
	}
}
