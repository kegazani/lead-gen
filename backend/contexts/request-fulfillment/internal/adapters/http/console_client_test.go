package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// consoleUser / consolePassword — учётные данные оператора консоли, задаваемые
// сборке среза в тестах, которым нужны консольные экраны.
const (
	consoleUser     = "operator"
	consolePassword = "secret"

	// consoleCookie — общее имя cookie сессии консоли (consoleauth.CookieName).
	// Консоли делят стор сессий и используют одно имя, поэтому вход в одну
	// действителен в другой. Дублируется строкой: пакет адаптера своё имя не
	// экспортирует, а тест держит ожидаемое значение рядом.
	consoleCookie = "session"
)

// consoleClient — HTTP-клиент консоли заявок: хранит cookie сессии и CSRF-токен.
// Редиректы не следуются, поэтому коды 303 и заголовок Location видны тесту.
type consoleClient struct {
	base    string
	session string
	csrf    string
}

// newConsoleClient создаёт клиента для запущенного тестового сервера.
func newConsoleClient(ts *httptest.Server) *consoleClient {
	return &consoleClient{base: ts.URL}
}

// login авторизует оператора и сохраняет cookie сессии и CSRF-токен.
func (c *consoleClient) login(t *testing.T) {
	t.Helper()
	resp := c.postForm(t, "/login", url.Values{
		"username": {consoleUser},
		"password": {consolePassword},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("Location после входа = %q, ожидался /", loc)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == consoleCookie {
			c.session = cookie.Value
			return
		}
	}
	t.Fatalf("сервер не вернул cookie %q", consoleCookie)
}

// do выполняет запрос, подставляя cookie сессии. htmx добавляет заголовок HTMX,
// чтобы обработчик отдал частичный ответ.
func (c *consoleClient) do(t *testing.T, method, path string, form url.Values, htmx bool) *http.Response {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: consoleCookie, Value: c.session})
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// get выполняет GET-запрос экрана.
func (c *consoleClient) get(t *testing.T, path string) *http.Response {
	return c.do(t, http.MethodGet, path, nil, false)
}

// postForm отправляет форму экрана.
func (c *consoleClient) postForm(t *testing.T, path string, form url.Values) *http.Response {
	return c.do(t, http.MethodPost, path, form, false)
}

// postFormHTMX отправляет форму экрана с заголовком HTMX.
func (c *consoleClient) postFormHTMX(t *testing.T, path string, form url.Values) *http.Response {
	return c.do(t, http.MethodPost, path, form, true)
}

// csrfForPage открывает экран и извлекает CSRF-токен из его формы. Токен постоянен
// для сессии, поэтому снимается один раз с любой страницы, где есть форма.
func (c *consoleClient) csrfForPage(t *testing.T, path string) string {
	t.Helper()
	resp := c.get(t, path)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("GET %s = %d, ожидался 200", path, resp.StatusCode)
	}
	html := readBody(t, resp)
	token := csrfFromHTML(html)
	if token == "" {
		t.Fatalf("CSRF-токен не найден на %s:\n%s", path, html)
	}
	return token
}

// noRedirectClient возвращает клиент, не следующий за перенаправлениями: тесту
// важны коды 303 и Location.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// csrfInputRe — скрытое поле CSRF-токена.
var csrfInputRe = regexp.MustCompile(`<input[^>]*name="csrf"[^>]*>`)

// csrfValueRe — атрибут value внутри поля.
var csrfValueRe = regexp.MustCompile(`value="([^"]*)"`)

// csrfFromHTML извлекает CSRF-токен из разметки формы.
func csrfFromHTML(html string) string {
	input := csrfInputRe.FindString(html)
	if input == "" {
		return ""
	}
	match := csrfValueRe.FindStringSubmatch(input)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
