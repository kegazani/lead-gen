package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/fulfillment"
	"lead-gen/backend/platform/consoleauth"
)

// TestConsoleScreensRequireSession проверяет, что консольные HTML-экраны без
// валидной cookie сессии редиректят на вход.
func TestConsoleScreensRequireSession(t *testing.T) {
	server := newScreenServer(t)
	client := noRedirectClient()

	for _, path := range []string{
		"/validation",
		"/requests/" + requestID + "/validation?tenant_id=" + tenantID,
		"/approvals",
		"/requests/" + requestID + "/approval?tenant_id=" + tenantID,
	} {
		resp, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("GET %s без сессии = %d, ожидался 303", path, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/login" {
			t.Fatalf("GET %s без сессии: Location = %q, ожидался /login", path, loc)
		}
		resp.Body.Close()
	}
}

// TestConsoleActionRequiresCSRF проверяет, что изменяющее действие консоли без
// CSRF-токена или с чужим токеном отклоняется, а с верным — выполняется.
func TestConsoleActionRequiresCSRF(t *testing.T) {
	server := newScreenServer(t)
	console := newConsoleClient(server)
	console.login(t)
	csrf := console.csrfForPage(t, validationScreenPath())
	action := "/requests/" + requestID + "/validation/take"

	resp := console.postForm(t, action, url.Values{"tenant_id": {tenantID}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST без CSRF = %d, ожидался 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = console.postForm(t, action, url.Values{"tenant_id": {tenantID}, "csrf": {"чужой-токен"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST с чужим CSRF = %d, ожидался 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = console.postForm(t, action, url.Values{"tenant_id": {tenantID}, "csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST с верным CSRF = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// TestContractRoutesStayAnonymous проверяет, что контрактные JSON-маршруты не
// гейтятся сессией и CSRF: без cookie они не редиректят на вход и не дают 403.
func TestContractRoutesStayAnonymous(t *testing.T) {
	server := newTestServer(t, fulfillment.NewMemoryQuery())
	client := noRedirectClient()

	submit, err := json.Marshal(map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"actor": "client", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if err != nil {
		t.Fatalf("сериализация: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/requests", strings.NewReader(string(submit)))
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /requests: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /requests без сессии = %d, ожидался 201: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	for _, path := range []string{
		"/requests/" + requestID + "?tenant_id=" + tenantID,
		"/requests/" + requestID + "/fulfillment-status?tenant_id=" + tenantID,
		"/requests/" + requestID + "/workflow",
	} {
		resp, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		assertAnonymous(t, "GET "+path, resp)
		resp.Body.Close()
	}

	// Контрактный переход (validation/start) анонимен и не конфликтует с
	// консольным validation/take.
	body := `{"tenant_id":"` + tenantID + `","actor":"client"}`
	req, err = http.NewRequest(http.MethodPost, server.URL+"/requests/"+requestID+"/validation/start", strings.NewReader(body))
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST validation/start: %v", err)
	}
	assertAnonymous(t, "POST validation/start", resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST validation/start без сессии = %d, ожидался 200", resp.StatusCode)
	}
	resp.Body.Close()
}

// assertAnonymous проверяет, что ответ не является ни редиректом на вход, ни
// отказом по CSRF.
func assertAnonymous(t *testing.T, label string, resp *http.Response) {
	t.Helper()
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatalf("%s гейтится сессией: 303 → %q", label, resp.Header.Get("Location"))
	}
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("%s требует CSRF: 403", label)
	}
	if loc := resp.Header.Get("Location"); loc == "/login" {
		t.Fatalf("%s редиректит на вход", label)
	}
}

// TestConsoleSessionCookieNameAndFlags проверяет имя и флаги cookie сессии консоли
// заявок. Имя общее с консолью lead-management ("session"): консоли делят общий
// стор сессий, поэтому вход в одну действителен в другой, а не затирает её.
func TestConsoleSessionCookieNameAndFlags(t *testing.T) {
	server := newScreenServer(t)
	form := url.Values{"username": {consoleUser}, "password": {consolePassword}}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}

	var session *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == consoleauth.CookieName {
			session = cookie
		}
	}
	if session == nil {
		t.Fatalf("cookie %q не установлен", consoleauth.CookieName)
	}
	if !session.HttpOnly {
		t.Fatalf("cookie сессии не HttpOnly")
	}
	if session.Path != "/" {
		t.Fatalf("Path = %q, ожидался /", session.Path)
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, ожидался Lax", session.SameSite)
	}
	if session.Secure {
		t.Fatalf("Secure выставлен на http-запросе")
	}
}

// TestSharedConsoleSessionAcrossServers — доказательство общего входа на уровне
// консоли: две сборки консоли делят ОДИН стор сессий. Сессия, полученная входом
// в первую, принимается второй без повторного входа, хотя учётные данные второй
// другие — проверка идёт только по общему стору.
func TestSharedConsoleSessionAcrossServers(t *testing.T) {
	store := consoleauth.NewMemoryStore()
	serverA := newStoreScreenServer(t, store, "op-a", "secret-a")
	serverB := newStoreScreenServer(t, store, "op-b", "secret-b")
	client := noRedirectClient()

	// Вход в первую консоль.
	form := url.Values{"username": {"op-a"}, "password": {"secret-a"}}
	req, err := http.NewRequest(http.MethodPost, serverA.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("сборка запроса входа: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("вход в первую консоль: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("вход в первую консоль = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	var session *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == consoleauth.CookieName {
			session = cookie
		}
	}
	if session == nil {
		t.Fatalf("первая консоль не выдала cookie %q", consoleauth.CookieName)
	}

	// Тот же cookie принимается второй консолью: повторного входа нет.
	req, err = http.NewRequest(http.MethodGet, serverB.URL+"/validation", nil)
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: session.Value})
	respB, err := client.Do(req)
	if err != nil {
		t.Fatalf("запрос во вторую консоль: %v", err)
	}
	defer respB.Body.Close()
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("вторая консоль с сессией первой = %d, ожидался 200: %s", respB.StatusCode, readBody(t, respB))
	}

	// Без сессии вторая консоль по-прежнему требует вход.
	anon, err := client.Get(serverB.URL + "/validation")
	if err != nil {
		t.Fatalf("анонимный запрос во вторую консоль: %v", err)
	}
	defer anon.Body.Close()
	if anon.StatusCode != http.StatusSeeOther {
		t.Fatalf("вторая консоль без сессии = %d, ожидался 303", anon.StatusCode)
	}
}

// newStoreScreenServer собирает консоль заявок с заданными учётными данными и
// общим стором сессий.
func newStoreScreenServer(t *testing.T, store consoleauth.Store, user, password string) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		ConsoleUser:     user,
		ConsolePassword: password,
		ConsoleStore:    store,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// mutableClock — часы, момент которых можно двигать вручную. Нужны для проверки
// истечения сессии по неактивности: срок проверяется не системным временем, а
// управляемым.
type mutableClock struct {
	mu sync.Mutex
	t  time.Time
}

// Now возвращает текущий момент часов.
func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance сдвигает момент часов вперёд на d.
func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newIdleScreenServer собирает сервер экрана валидации с поданной заявкой и
// настраиваемым сроком неактивности сессии. Часы сессии управляемые, поэтому
// истечение проверяется без ожидания реального времени.
func newIdleScreenServer(t *testing.T, idle time.Duration, clock *mutableClock) *httptest.Server {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		ConsoleUser:        consoleUser,
		ConsolePassword:    consolePassword,
		ConsoleIdleTimeout: idle,
		Now:                clock.Now,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)

	resp := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": requestID, "offer_id": "offer-1", "tenant_id": tenantID,
		"actor": "client", "content": "Предлагаем автоматизацию найма", "target": "IT-компании",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание заявки = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	return server
}

// TestConsoleIdleExpiry проверяет истечение сессии оператора по неактивности.
// Пока оператор активен, экран валидации отдаётся; после паузы дольше
// IdleTimeout запрос уводит на страницу входа, и данные экрана уже не
// выдаются (тело ответа — редирект, а не разметка с заявкой). Активность до
// истечения продлевает сессию.
func TestConsoleIdleExpiry(t *testing.T) {
	clock := &mutableClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	server := newIdleScreenServer(t, time.Minute, clock)
	console := newConsoleClient(server)
	console.login(t)

	// Сразу после входа экран отдаётся.
	fresh := console.get(t, "/validation")
	if fresh.StatusCode != http.StatusOK {
		t.Fatalf("GET /validation сразу после входа = %d, ожидался 200: %s", fresh.StatusCode, readBody(t, fresh))
	}
	fresh.Body.Close()

	// Активность в пределах окна продлевает сессию.
	clock.advance(45 * time.Second)
	active := console.get(t, "/validation")
	if active.StatusCode != http.StatusOK {
		t.Fatalf("GET /validation после активности = %d, ожидался 200: %s", active.StatusCode, readBody(t, active))
	}
	active.Body.Close()

	// Пауза дольше IdleTimeout исчерпывает сессию: экран больше не отдаётся.
	clock.advance(61 * time.Second)
	expired := console.get(t, "/validation")
	if expired.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /validation после истечения = %d, ожидался 303: %s", expired.StatusCode, readBody(t, expired))
	}
	if loc := expired.Header.Get("Location"); loc != "/login" {
		t.Fatalf("Location после истечения = %q, ожидался /login", loc)
	}
	body := readBody(t, expired)
	if strings.Contains(body, "Предлагаем автоматизацию найма") {
		t.Fatalf("после истечения данные экрана отданы в теле ответа:\n%s", body)
	}
}

// TestConsoleLogoutClearsSession проверяет, что выход снимает cookie сессии и
// последующий заход на экран снова требует вход.
func TestConsoleLogoutClearsSession(t *testing.T) {
	server := newScreenServer(t)
	console := newConsoleClient(server)
	console.login(t)

	resp := console.get(t, "/logout")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /logout = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("Location после выхода = %q, ожидался /login", loc)
	}
	var cleared *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == consoleCookie {
			cleared = cookie
		}
	}
	if cleared == nil || cleared.MaxAge != -1 {
		t.Fatalf("выход не снял cookie %q: %+v", consoleCookie, cleared)
	}
	resp.Body.Close()

	after := console.get(t, "/validation")
	if after.StatusCode != http.StatusSeeOther || after.Header.Get("Location") != "/login" {
		t.Fatalf("после выхода GET /validation = %d %q, ожидался 303 /login", after.StatusCode, after.Header.Get("Location"))
	}
	after.Body.Close()
}
