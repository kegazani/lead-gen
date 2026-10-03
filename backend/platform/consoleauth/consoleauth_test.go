package consoleauth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/platform/consoleauth"
)

// mutableClock — часы, момент которых можно двигать вручную. Нужны для проверки
// истечения сессии по неактивности.
type mutableClock struct {
	mu sync.Mutex
	t  time.Time
}

// Now возвращает текущий (двигаемый) момент.
func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance сдвигает момент вперёд на d.
func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newManager собирает менеджер с оператором «operator / secret».
func newManager() *consoleauth.Manager {
	return consoleauth.New(consoleauth.Config{Username: "operator", Password: "secret"})
}

// TestLoginSuccess проверяет, что верные учётные данные дают сессию с логином и
// непустым CSRF-токеном.
func TestLoginSuccess(t *testing.T) {
	mgr := newManager()
	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(id) != 64 {
		t.Fatalf("длина идентификатора сессии = %d, ожидалось 64", len(id))
	}
	sess, ok := mgr.Authenticate(id)
	if !ok {
		t.Fatalf("Authenticate валидной сессии = false")
	}
	if sess.Username() != "operator" {
		t.Fatalf("Username = %q", sess.Username())
	}
	if len(sess.CSRF()) != 64 {
		t.Fatalf("длина CSRF = %d, ожидалось 64", len(sess.CSRF()))
	}
}

// TestLoginRejectsWrongCredentials проверяет, что неверные учётные данные дают
// ErrInvalidCredentials и не создают сессию.
func TestLoginRejectsWrongCredentials(t *testing.T) {
	mgr := newManager()
	cases := []struct {
		name     string
		username string
		password string
	}{
		{"неверный пароль", "operator", "неверный"},
		{"неверный логин", "кто-то", "secret"},
		{"оба неверны", "кто-то", "неверный"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := mgr.Login(tc.username, tc.password)
			if !errors.Is(err, consoleauth.ErrInvalidCredentials) {
				t.Fatalf("Login: err = %v, ожидалась ErrInvalidCredentials", err)
			}
			if id != "" {
				t.Fatalf("Login вернул идентификатор %q при отказе", id)
			}
		})
	}
}

// TestAuthenticateUnknown проверяет, что неизвестный идентификатор не даёт сессии.
func TestAuthenticateUnknown(t *testing.T) {
	mgr := newManager()
	if _, ok := mgr.Authenticate("нет-такой-сессии"); ok {
		t.Fatalf("Authenticate неизвестной сессии = true")
	}
}

// TestLogout проверяет, что после выхода сессия невалидна (идемпотентно).
func TestLogout(t *testing.T) {
	mgr := newManager()
	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	mgr.Logout(id)
	if _, ok := mgr.Authenticate(id); ok {
		t.Fatalf("Authenticate после Logout = true")
	}
	mgr.Logout(id) // повторный выход не паникует
}

// TestSessionExpiresByIdle проверяет истечение по неактивности и продление при
// активности.
func TestSessionExpiresByIdle(t *testing.T) {
	start := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	clock := &mutableClock{t: start}
	mgr := consoleauth.New(consoleauth.Config{
		Username:    "operator",
		Password:    "secret",
		IdleTimeout: time.Minute,
		Now:         clock.Now,
	})
	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	clock.advance(30 * time.Second)
	if _, ok := mgr.Authenticate(id); !ok {
		t.Fatalf("сессия истекла раньше времени")
	}
	// Активность продлила сессию: ещё 30 секунд бездействия её не убивают.
	clock.advance(30 * time.Second)
	if _, ok := mgr.Authenticate(id); !ok {
		t.Fatalf("сессия истекла, хотя активность продлила её")
	}
	// Период неактивности превышен.
	clock.advance(61 * time.Second)
	if _, ok := mgr.Authenticate(id); ok {
		t.Fatalf("сессия не истекла по неактивности")
	}
}

// TestDefaultIdleTimeout проверяет, что нулевой IdleTimeout даёт значение по
// умолчанию.
func TestDefaultIdleTimeout(t *testing.T) {
	if consoleauth.DefaultIdleTimeout != 30*time.Minute {
		t.Fatalf("DefaultIdleTimeout = %v", consoleauth.DefaultIdleTimeout)
	}
	start := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	clock := &mutableClock{t: start}
	mgr := consoleauth.New(consoleauth.Config{Username: "operator", Password: "secret", Now: clock.Now})
	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	clock.advance(consoleauth.DefaultIdleTimeout + time.Second)
	if _, ok := mgr.Authenticate(id); ok {
		t.Fatalf("сессия не истекла по значению по умолчанию")
	}
}

// okHandler отвечает 200 и записывает логин сессии из контекста (пусто, если её
// нет) — так тест видит и результат middleware, и проброс сессии.
func okHandler(seen *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess := consoleauth.Current(r.Context()); sess != nil {
			*seen = sess.Username()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ок"))
	})
}

// policy собирает политику, повторяющую консоль lead-management.
func policy() consoleauth.Policy {
	return consoleauth.Policy{
		LoginPath:      "/login",
		Public:         []string{"/login", "/healthz", "/lead-search", "/email-generation"},
		PublicPrefixes: []string{"/static/"},
		CSRFExempt:     []string{"/logout"},
	}
}

// TestMiddlewareRedirectsWithoutSession проверяет, что запрос без сессии
// редиректится на страницу входа.
func TestMiddlewareRedirectsWithoutSession(t *testing.T) {
	mgr := newManager()
	var seen string
	handler := mgr.Middleware(policy())(okHandler(&seen))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("статус = %d, ожидался 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, ожидался /login", loc)
	}
	if seen != "" {
		t.Fatalf("обработчик вызван без сессии")
	}
}

// TestMiddlewarePassesSessionIntoContext проверяет, что валидная сессия попадает
// в контекст, а недействительная — редиректит.
func TestMiddlewarePassesSessionIntoContext(t *testing.T) {
	mgr := newManager()
	var seen string
	handler := mgr.Middleware(policy())(okHandler(&seen))

	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: id})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("статус с сессией = %d, ожидался 200", rec.Code)
	}
	if seen != "operator" {
		t.Fatalf("логин из контекста = %q", seen)
	}

	// Неизвестный идентификатор сессии — отказ.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: "чужой-токен"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("статус с чужой cookie = %d, ожидался 303", rec.Code)
	}
}

// TestMiddlewarePublicPaths проверяет, что публичные пути и префикс статики
// проходят без сессии.
func TestMiddlewarePublicPaths(t *testing.T) {
	mgr := newManager()
	var seen string
	handler := mgr.Middleware(policy())(okHandler(&seen))

	for _, path := range []string{"/login", "/healthz", "/lead-search", "/email-generation", "/static/app.js", "/static/css/console.css"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, ожидался 200 без сессии", path, rec.Code)
		}
	}
}

// TestMiddlewareRequiresCSRF проверяет, что изменяющий запрос без токена или с
// неверным токеном отклоняется, а с верным — проходит.
func TestMiddlewareRequiresCSRF(t *testing.T) {
	mgr := newManager()
	var seen string
	handler := mgr.Middleware(policy())(okHandler(&seen))
	id, _ := mgr.Login("operator", "secret")
	sess, ok := mgr.Authenticate(id)
	if !ok {
		t.Fatalf("Authenticate: сессия не создана")
	}

	// GET не требует токена.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodGet, "/search", id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search = %d, ожидался 200", rec.Code)
	}

	// POST без токена — отказ по CSRF.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodPost, "/search", id, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /search без CSRF = %d, ожидался 403", rec.Code)
	}

	// POST с неверным токеном — отказ.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodPost, "/search", id, url.Values{"csrf": {"неверный"}}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /search с неверным CSRF = %d, ожидался 403", rec.Code)
	}

	// POST с верным токеном — проходит.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodPost, "/search", id, url.Values{"csrf": {sess.CSRF()}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /search с верным CSRF = %d, ожидался 200", rec.Code)
	}
}

// TestMiddlewareCSRFExempt проверяет, что на пути выхода проверка CSRF пропускается.
func TestMiddlewareCSRFExempt(t *testing.T) {
	mgr := newManager()
	var seen string
	handler := mgr.Middleware(policy())(okHandler(&seen))
	id, _ := mgr.Login("operator", "secret")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodPost, "/logout", id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /logout без CSRF = %d, ожидался 200 (выход без токена)", rec.Code)
	}
}

// TestMiddlewareCustomCSRFError проверяет, что заданный OnCSRFError рисует отказ
// вместо значения по умолчанию.
func TestMiddlewareCustomCSRFError(t *testing.T) {
	mgr := newManager()
	called := false
	pol := policy()
	pol.OnCSRFError = func(w http.ResponseWriter, _ *http.Request) {
		called = true
		http.Error(w, "своя страница отказа", http.StatusTeapot)
	}
	var seen string
	handler := mgr.Middleware(pol)(okHandler(&seen))
	id, _ := mgr.Login("operator", "secret")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodPost, "/search", id, nil))
	if !called {
		t.Fatalf("OnCSRFError не вызван")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("статус = %d, ожидался 418", rec.Code)
	}
}

// TestSetSessionCookieFlags проверяет атрибуты cookie сессии, включая Secure по
// защищённому каналу.
func TestSetSessionCookieFlags(t *testing.T) {
	cases := []struct {
		name   string
		target string
		header map[string]string
		secure bool
	}{
		{"http не secure", "http://console.example/", nil, false},
		{"https secure", "https://console.example/", nil, true},
		{"обратный прокси", "http://console.example/", map[string]string{"X-Forwarded-Proto": "https"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			consoleauth.SetSessionCookie(rec, req, "id-1")

			resp := rec.Result()
			cookies := resp.Cookies()
			if len(cookies) != 1 {
				t.Fatalf("cookie = %d, ожидалась 1", len(cookies))
			}
			c := cookies[0]
			if c.Name != consoleauth.CookieName {
				t.Fatalf("имя cookie = %q", c.Name)
			}
			if c.Value != "id-1" {
				t.Fatalf("значение cookie = %q", c.Value)
			}
			if !c.HttpOnly {
				t.Fatalf("cookie не HttpOnly")
			}
			if c.Secure != tc.secure {
				t.Fatalf("Secure = %v, ожидалось %v", c.Secure, tc.secure)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("SameSite = %v, ожидался Lax", c.SameSite)
			}
			if c.Path != "/" {
				t.Fatalf("Path = %q, ожидался /", c.Path)
			}
		})
	}
}

// TestClearSessionCookie проверяет, что очистка ставит MaxAge=-1 и те же флаги.
func TestClearSessionCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://console.example/", nil)
	rec := httptest.NewRecorder()
	consoleauth.ClearSessionCookie(rec, req)

	resp := rec.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie = %d, ожидалась 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != consoleauth.CookieName {
		t.Fatalf("имя cookie = %q", c.Name)
	}
	if c.MaxAge != -1 {
		t.Fatalf("MaxAge = %d, ожидался -1", c.MaxAge)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("флаги очистки cookie = %+v", c)
	}
}

// TestConfigurableCookieName проверяет настраиваемое имя cookie: менеджер ставит
// и читает cookie под своим именем. Общим значением по умолчанию это имя больше
// не требуется — консоли делят общий cookie "session"; тест фиксирует, что
// настройка осталась возможной.
func TestConfigurableCookieName(t *testing.T) {
	mgr := consoleauth.New(consoleauth.Config{
		Username:   "operator",
		Password:   "secret",
		CookieName: "console_custom",
	})
	if mgr.CookieName() != "console_custom" {
		t.Fatalf("CookieName = %q, ожидалось console_custom", mgr.CookieName())
	}

	// Метод ставит cookie под настроенным именем.
	req := httptest.NewRequest(http.MethodGet, "https://console.example/", nil)
	rec := httptest.NewRecorder()
	mgr.SetSessionCookie(rec, req, "id-1")
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "console_custom" || cookies[0].Value != "id-1" {
		t.Fatalf("cookie сессии = %+v", cookies)
	}

	// Очистка снимает то же имя.
	rec = httptest.NewRecorder()
	mgr.ClearSessionCookie(rec, req)
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != "console_custom" || cleared[0].MaxAge != -1 {
		t.Fatalf("очистка cookie = %+v", cleared)
	}

	// Middleware читает сессию именно по своему имени cookie: чужое имя
	// (cookie по умолчанию) доступа не даёт.
	var seen string
	handler := mgr.Middleware(consoleauth.Policy{LoginPath: "/login"})(okHandler(&seen))
	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: id})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("чужое имя cookie: статус = %d, ожидался 303", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "console_custom", Value: id})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || seen != "operator" {
		t.Fatalf("своё имя cookie: статус = %d, логин = %q", rec.Code, seen)
	}

	// Пустое имя даёт значение по умолчанию.
	if def := consoleauth.New(consoleauth.Config{Username: "operator", Password: "secret"}); def.CookieName() != consoleauth.CookieName {
		t.Fatalf("CookieName по умолчанию = %q", def.CookieName())
	}
}

// TestCSRFHelper проверяет поведение CSRF(r) без сессии и с ней.
func TestCSRFHelper(t *testing.T) {
	mgr := newManager()
	var inner string
	handler := mgr.Middleware(policy())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner = consoleauth.CSRF(r)
		w.WriteHeader(http.StatusOK)
	}))
	id, _ := mgr.Login("operator", "secret")
	sess, _ := mgr.Authenticate(id)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodGet, "/", id, nil))
	if inner != sess.CSRF() {
		t.Fatalf("CSRF = %q, ожидался %q", inner, sess.CSRF())
	}

	// Без сессии — пусто.
	if got := consoleauth.CSRF(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Fatalf("CSRF без сессии = %q, ожидалось пусто", got)
	}
}

// authRequest собирает запрос с cookie сессии и, при необходимости, телом формы с
// CSRF-токеном.
func authRequest(method, path, sessionID string, form url.Values) *http.Request {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: sessionID})
	return req
}
