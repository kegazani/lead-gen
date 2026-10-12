package httpapi_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// sidebarHTML вырезает контейнер боковой навигации из отрендеренной страницы.
// Проверки состава навигации идут по этому фрагменту, чтобы ссылки внутри
// основного содержимого (CTA, форма) не подменяли пункты меню.
func sidebarHTML(page string) string {
	const open = `<nav class="sidebar"`
	i := strings.Index(page, open)
	if i < 0 {
		return ""
	}
	j := strings.Index(page[i:], "</nav>")
	if j < 0 {
		return page[i:]
	}
	return page[i : i+j]
}

// mainHTML вырезает основное содержимое страницы (после <main>). CTA живут
// именно там, а не в боковой навигации.
func mainHTML(page string) string {
	i := strings.Index(page, "<main")
	if i < 0 {
		return ""
	}
	return page[i:]
}

// fieldServerSideClasses сопоставляет имя поля формы с суффиксом класса его
// абзаца <p class="field ...">. По этому суффиксу проверяется span-2 у полей
// описания и юридических данных.
var fieldServerSideClasses = regexp.MustCompile(`(?s)<p class="field([^"]*)">(.*?)</p>`)
var fieldNameAttr = regexp.MustCompile(`name="([^"]+)"`)

func fieldClasses(page string) map[string]string {
	classes := map[string]string{}
	for _, m := range fieldServerSideClasses.FindAllStringSubmatch(page, -1) {
		name := fieldNameAttr.FindStringSubmatch(m[2])
		if name == nil {
			continue
		}
		classes[name[1]] = m[1]
	}
	return classes
}

// TestAnonymousPagesHaveNoWorkspaceSidebar проверяет, что на анонимных экранах
// нет рабочего бокового меню и что они отданы в auth-карточке.
func TestAnonymousPagesHaveNoWorkspaceSidebar(t *testing.T) {
	server := newServer(t, newFakeRequests())
	for _, path := range []string{"/login", "/register", "/reset"} {
		page := body(t, get(t, server, path))
		if nav := sidebarHTML(page); nav != "" {
			t.Fatalf("GET %s: у анонима не должно быть рабочего сайдбара:\n%s", path, nav)
		}
		if !strings.Contains(page, `class="auth-card`) {
			t.Fatalf("GET %s: страница не отдана в auth-карточке", path)
		}
	}
}

// TestAnonymousAuthSwitch проверяет сегментированный выбор «Вход | Регистрация»
// в auth-карточке и правильную активную вкладку.
func TestAnonymousAuthSwitch(t *testing.T) {
	server := newServer(t, newFakeRequests())

	login := body(t, get(t, server, "/login"))
	if !strings.Contains(login, `class="auth-card`) || !strings.Contains(login, `class="tabs"`) {
		t.Fatalf("GET /login: нет auth-карточки с переключателем")
	}
	for _, want := range []string{`href="/login"`, `href="/register"`} {
		if !strings.Contains(login, want) {
			t.Fatalf("GET /login: переключатель не содержит %s", want)
		}
	}
	if !strings.Contains(login, `href="/login" aria-current="page"`) {
		t.Fatalf("GET /login: вкладка входа не отмечена активной")
	}

	register := body(t, get(t, server, "/register"))
	if !strings.Contains(register, `href="/register" aria-current="page"`) {
		t.Fatalf("GET /register: вкладка регистрации не отмечена активной")
	}
}

// TestAnonymousPagesHaveNoProtectedLinks проверяет, что во всём HTML анонимных
// экранов нет ссылок в защищённый портал.
func TestAnonymousPagesHaveNoProtectedLinks(t *testing.T) {
	server := newServer(t, newFakeRequests())
	for _, path := range []string{"/login", "/register", "/reset"} {
		page := body(t, get(t, server, path))
		for _, forbidden := range []string{`href="/portal"`, `href="/profile"`, `href="/portal/request"`} {
			if strings.Contains(page, forbidden) {
				t.Fatalf("GET %s: страница анонима содержит %s", path, forbidden)
			}
		}
	}
}

// TestRegisterSentScreenNavigation проверяет, что экран подтверждения
// регистрации (отдаётся после успешного POST /register) не показывает рабочее
// меню и даёт ссылку на вход.
func TestRegisterSentScreenNavigation(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := postForm(t, server, "/register", url.Values{
		"company_name":  {"ООО Ромашка"},
		"description":   {"Поставщик промышленного оборудования"},
		"email":         {"client@romashka.ru"},
		"password":      {"secret"},
		"inn":           {"7701234567"},
		"company_email": {"hello@romashka.ru"},
		"website":       {"https://romashka.ru"},
		"legal_data":    {"ООО «Ромашка»"},
	})
	page := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /register = %d, ожидался 200", resp.StatusCode)
	}
	if nav := sidebarHTML(page); nav != "" {
		t.Fatalf("экран подтверждения: не должно быть рабочего сайдбара:\n%s", nav)
	}
	if !strings.Contains(mainHTML(page), `href="/login"`) {
		t.Fatalf("экран подтверждения: нет ссылки на вход")
	}
	if !strings.Contains(page, `class="auth-card`) {
		t.Fatalf("экран подтверждения: нет auth-карточки")
	}
}

// TestAnonymousCTAs проверяет взаимные переходы между экранами входа,
// регистрации, сброса и подтверждения — ссылка находится в основном
// содержимом (auth-cta/абзац), а не в боковой навигации.
func TestAnonymousCTAs(t *testing.T) {
	server := newServer(t, newFakeRequests())

	login := body(t, get(t, server, "/login"))
	if !strings.Contains(mainHTML(login), `href="/register"`) {
		t.Fatalf("GET /login: нет CTA на регистрацию в основном содержимом")
	}
	if !strings.Contains(login, `class="auth-cta"`) {
		t.Fatalf("GET /login: CTA не оформлен классом auth-cta")
	}

	register := body(t, get(t, server, "/register"))
	if !strings.Contains(mainHTML(register), `href="/login"`) {
		t.Fatalf("GET /register: нет CTA на вход в основном содержимом")
	}
	if !strings.Contains(register, `class="auth-cta"`) {
		t.Fatalf("GET /register: CTA не оформлен классом auth-cta")
	}

	reset := body(t, get(t, server, "/reset"))
	if !strings.Contains(mainHTML(reset), `href="/login"`) {
		t.Fatalf("GET /reset: нет CTA на вход в основном содержимом")
	}
	if !strings.Contains(reset, `class="auth-cta"`) {
		t.Fatalf("GET /reset: CTA не оформлен классом auth-cta")
	}

	resp := postForm(t, server, "/register", url.Values{
		"company_name":  {"ООО Ромашка"},
		"description":   {"Поставщик промышленного оборудования"},
		"email":         {"client@romashka.ru"},
		"password":      {"secret"},
		"inn":           {"7701234567"},
		"company_email": {"hello@romashka.ru"},
	})
	sent := body(t, resp)
	if !strings.Contains(mainHTML(sent), `href="/login"`) {
		t.Fatalf("экран подтверждения регистрации: нет ссылки на вход")
	}
}

// TestRegisterFormStructure проверяет структурированную форму регистрации: три
// секции fieldset с legend, все восемь полей и span-2 у полноширинных полей
// (компания, описание, сайт, юридические данные).
func TestRegisterFormStructure(t *testing.T) {
	server := newServer(t, newFakeRequests())
	page := body(t, get(t, server, "/register"))

	if got := strings.Count(page, `<fieldset class="form-section">`); got != 3 {
		t.Fatalf("секций fieldset.form-section = %d, ожидалось 3:\n%s", got, page)
	}
	for _, legend := range []string{"Компания", "Учётная запись", "Реквизиты компании"} {
		if !strings.Contains(page, "<legend>"+legend+"</legend>") {
			t.Fatalf("нет legend-секции %q", legend)
		}
	}

	for _, name := range []string{
		"company_name", "description", "email", "password",
		"inn", "company_email", "website", "legal_data",
	} {
		if !strings.Contains(page, `name="`+name+`"`) {
			t.Fatalf("форма регистрации не содержит поле %q", name)
		}
	}

	classes := fieldClasses(page)
	for _, name := range []string{"company_name", "description", "website", "legal_data"} {
		if !strings.Contains(classes[name], "span-2") {
			t.Fatalf("поле %q: класс %q не содержит span-2", name, classes[name])
		}
	}
	span2 := 0
	for _, class := range classes {
		if strings.Contains(class, "span-2") {
			span2++
		}
	}
	if span2 != 4 {
		t.Fatalf("полей с span-2 = %d, ожидалось 4", span2)
	}
}

// TestAuthenticatedNavigation проверяет навигацию вошедшего клиента на всех
// защищённых экранах: три пункта Портал/Профиль/Заявка, активный пункт и
// отсутствие анонимных пунктов, переключатель темы сохранён.
func TestAuthenticatedNavigation(t *testing.T) {
	slice, server := newAuthServer(t, newFakeRequests())
	registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	cookie := login(t, server, "client@romashka.ru", "secret")

	for _, path := range []string{"/portal", "/profile", "/portal/request"} {
		page := body(t, getCookie(t, server, path, cookie))
		nav := sidebarHTML(page)
		if nav == "" {
			t.Fatalf("GET %s: контейнер навигации не найден", path)
		}
		for _, want := range []string{`href="/portal"`, `href="/profile"`, `href="/portal/request"`} {
			if !strings.Contains(nav, want) {
				t.Fatalf("GET %s: навигация клиента не содержит %s:\n%s", path, want, nav)
			}
		}
		for _, forbidden := range []string{`href="/login"`, `href="/register"`} {
			if strings.Contains(nav, forbidden) {
				t.Fatalf("GET %s: навигация клиента содержит анонимный пункт %s:\n%s", path, forbidden, nav)
			}
		}
		active := `<a href="` + path + `" class="is-active" aria-current="page">`
		if !strings.Contains(nav, active) {
			t.Fatalf("GET %s: пункт не отмечен активным:\n%s", path, nav)
		}
		if !strings.Contains(nav, `class="theme-toggle"`) {
			t.Fatalf("GET %s: нет переключателя темы", path)
		}
	}
}

// TestRegisterEmptyFormShowsValidation проверяет, что POST /register с пустыми
// полями возвращает 400 с ошибками валидации и самой формой, а не 500.
func TestRegisterEmptyFormShowsValidation(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := postForm(t, server, "/register", url.Values{})
	page := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("пустая регистрация = %d, ожидался 400", resp.StatusCode)
	}
	if !strings.Contains(page, `action="/register"`) {
		t.Fatalf("экран ошибки не содержит форму регистрации")
	}
	for _, msg := range []string{
		"Укажите название компании",
		"Укажите описание компании",
		"Укажите email учётной записи",
		"Укажите пароль",
		"Укажите ИНН компании",
		"Укажите email компании",
	} {
		if !strings.Contains(page, msg) {
			t.Fatalf("экран ошибки не показал сообщение %q:\n%s", msg, page)
		}
	}
}
