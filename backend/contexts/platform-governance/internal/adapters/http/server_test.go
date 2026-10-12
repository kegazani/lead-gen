package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// fakeRequests — управляемый порт контракта request-intent: помнит поданные
// заявки по тенанту и может вернуть сырые поля оффера, чтобы проверить
// маскирование.
type fakeRequests struct {
	mu           sync.Mutex
	byTenant     map[string]string
	rawContent   string
	rawTarget    string
	status       string
	errorReason  string
	errorMessage string
	statusErr    error
}

func newFakeRequests() *fakeRequests {
	return &fakeRequests{byTenant: map[string]string{}, status: "submitted"}
}

// Submit реализует requestintent.Port.
func (f *fakeRequests) Submit(_ context.Context, in requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
	f.mu.Lock()
	f.byTenant[in.TenantID] = in.RequestID
	f.mu.Unlock()
	return requestintent.SubmitResult{RequestID: in.RequestID, Status: "submitted"}, nil
}

// Status реализует requestintent.Port. Заявка чужого тенанта неотличима от
// отсутствующей: возвращается ErrNotFound.
func (f *fakeRequests) Status(_ context.Context, q requestintent.StatusQuery) (requestintent.StatusResult, error) {
	if f.statusErr != nil {
		return requestintent.StatusResult{}, f.statusErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	requestID, ok := f.byTenant[q.TenantID]
	if !ok || requestID != q.RequestID {
		return requestintent.StatusResult{}, requestintent.ErrNotFound
	}
	return requestintent.StatusResult{
		RequestID:    requestID,
		Status:       f.status,
		ErrorReason:  f.errorReason,
		ErrorMessage: f.errorMessage,
		OfferContent: f.rawContent,
		OfferTarget:  f.rawTarget,
	}, nil
}

// List реализует requestintent.Port.
func (f *fakeRequests) List(_ context.Context, q requestintent.ListQuery) ([]requestintent.ListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	requestID, ok := f.byTenant[q.TenantID]
	if !ok {
		return nil, requestintent.ErrNotFound
	}
	return []requestintent.ListEntry{{RequestID: requestID, Title: "Заявка клиента", Status: "submitted"}}, nil
}

// fixedClock — детерминированные часы теста.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// seqIDs — детерминированный генератор идентификаторов.
type seqIDs struct{ n int }

func (s *seqIDs) next(prefix string) string {
	s.n++
	return prefix + "-" + strconv.Itoa(s.n)
}

func (s *seqIDs) NewTenantID() domain.TenantID { return domain.TenantID(s.next("tenant")) }
func (s *seqIDs) NewUserID() domain.UserID     { return domain.UserID(s.next("user")) }
func (s *seqIDs) NewEventID() domain.EventID   { return domain.EventID(s.next("evt")) }
func (s *seqIDs) NewAuditRecordID() domain.AuditRecordID {
	return domain.AuditRecordID(s.next("audit"))
}
func (s *seqIDs) NewRequestID() string { return s.next("req") }
func (s *seqIDs) NewOfferID() string   { return s.next("offer") }

// newServer собирает HTTP-сервер портала поверх заданного контракта (без прав).
func newServer(t *testing.T, requests requestintent.Port) *httptest.Server {
	t.Helper()
	slice, err := platformgovernance.Build(platformgovernance.Config{Requests: requests})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// newAuthServer собирает сервер с правами администратора и детерминированными
// идентификаторами, чтобы тесты могли регистрировать и одобрять тенанты.
func newAuthServer(t *testing.T, requests requestintent.Port) (*platformgovernance.Slice, *httptest.Server) {
	t.Helper()
	adminGrants, err := grants.FromStrings("admin-1", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{
		Grants:   adminGrants,
		Requests: requests,
		Clock:    fixedClock{at: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		IDs:      &seqIDs{},
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return slice, server
}

// registerClient подаёт заявку на регистрацию через команду и одобряет её.
func registerAndApprove(t *testing.T, slice *platformgovernance.Slice, email, password, inn, companyEmail string) (string, string) {
	t.Helper()
	result, err := slice.RegisterClient.Handle(context.Background(), command.RegisterClient{
		CompanyName:  "ООО Ромашка",
		Description:  "Поставщик промышленного оборудования",
		Email:        email,
		Password:     password,
		INN:          inn,
		CompanyEmail: companyEmail,
		Website:      "https://romashka.ru",
		LegalData:    "ООО «Ромашка»",
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if err := slice.ApproveRegistration.Handle(context.Background(), command.ApproveRegistration{
		TenantID: result.TenantID, Actor: "admin-1", CommandID: "cmd-approve-" + result.TenantID,
	}); err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	return result.TenantID, result.UserID
}

// login выполняет вход и возвращает cookie сессии портала.
func login(t *testing.T, server *httptest.Server, email, password string) *http.Cookie {
	t.Helper()
	resp := postForm(t, server, "/login", url.Values{"email": {email}, "password": {password}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("вход = %d, ожидался 303: %s", resp.StatusCode, body(t, resp))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "portal_session" {
			return c
		}
	}
	t.Fatalf("cookie portal_session не установлен: %v", resp.Cookies())
	return nil
}

// noRedirectClient возвращает клиент, не следующий за перенаправлениями.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// get выполняет GET-запрос, следуя за перенаправлениями.
func get(t *testing.T, server *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := server.Client().Get(server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// getCookie выполняет GET-запрос с cookie сессии, не следуя за перенаправлениями.
func getCookie(t *testing.T, server *httptest.Server, path string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// postForm отправляет форму и не следует за перенаправлением.
func postForm(t *testing.T, server *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := noRedirectClient().PostForm(server.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// postFormCookie отправляет форму с cookie сессии и не следует за перенаправлением.
func postFormCookie(t *testing.T, server *httptest.Server, path string, form url.Values, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// body читает тело ответа и закрывает его.
func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	return string(data)
}

// TestHealthz проверяет, что сервис отвечает 200 на проверку живости.
func TestHealthz(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := get(t, server, "/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz = %d", resp.StatusCode)
	}
}

// TestRootRedirectsToLogin проверяет, что корень сервиса ведёт на вход.
func TestRootRedirectsToLogin(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := getCookie(t, server, "/", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("GET / = %d, Location = %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// TestRegisterLinksSharedStylesheet проверяет, что экран портала подключает общее
// оформление компонентов и оно реально раздаётся.
func TestRegisterLinksSharedStylesheet(t *testing.T) {
	server := newServer(t, newFakeRequests())
	page := body(t, get(t, server, "/register"))
	if !strings.Contains(page, `<link rel="stylesheet" href="/static/console.css">`) {
		t.Fatalf("экран не подключил общее оформление:\n%s", page)
	}
}

// TestRegisterScreen проверяет рендеринг формы регистрации с новыми полями.
func TestRegisterScreen(t *testing.T) {
	server := newServer(t, newFakeRequests())
	page := body(t, get(t, server, "/register"))
	for _, label := range []string{"Email учётной записи", "Пароль", "ИНН компании", "Email компании", "Сайт компании"} {
		if !strings.Contains(page, label) {
			t.Fatalf("форма не показала поле %q:\n%s", label, page)
		}
	}
}

// TestRegisterSubmit проверяет успешную регистрацию: экран подтверждения, а не
// переход в портал.
func TestRegisterSubmit(t *testing.T) {
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("регистрация = %d, ожидался 200: %s", resp.StatusCode, body(t, resp))
	}
	if !strings.Contains(body(t, resp), "Заявка на регистрацию отправлена") {
		t.Fatalf("нет экрана подтверждения")
	}
}

// TestRegisterIncomplete проверяет неполную регистрацию: экран указывает на
// недостающее поле, а не отдаёт 500.
func TestRegisterIncomplete(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := postForm(t, server, "/register", url.Values{
		"company_name":  {"ООО Ромашка"},
		"email":         {"client@romashka.ru"},
		"password":      {"secret"},
		"inn":           {"7701234567"},
		"company_email": {"hello@romashka.ru"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("неполная регистрация = %d, ожидался 400", resp.StatusCode)
	}
	if !strings.Contains(body(t, resp), "Укажите описание компании") {
		t.Fatalf("экран не указал недостающее поле")
	}
}

// TestRegisterDuplicate проверяет отклонение повторной заявки с тем же ИНН.
func TestRegisterDuplicate(t *testing.T) {
	slice, server := newAuthServer(t, newFakeRequests())
	registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")

	resp := postForm(t, server, "/register", url.Values{
		"company_name":  {"Другая компания"},
		"description":   {"Описание"},
		"email":         {"other@romashka.ru"},
		"password":      {"secret"},
		"inn":           {"7701234567"},
		"company_email": {"other-company@romashka.ru"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("дубль = %d, ожидался 409", resp.StatusCode)
	}
	if !strings.Contains(body(t, resp), "уже зарегистрирована") {
		t.Fatalf("нет сообщения о дубле")
	}
}

// TestLoginLogout проверяет вход с установкой сессии и выход.
func TestLoginLogout(t *testing.T) {
	slice, server := newAuthServer(t, newFakeRequests())
	registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")

	cookie := login(t, server, "client@romashka.ru", "secret")

	portal := getCookie(t, server, "/portal", cookie)
	defer portal.Body.Close()
	if portal.StatusCode != http.StatusOK {
		t.Fatalf("портал = %d, ожидался 200", portal.StatusCode)
	}

	logout := postFormCookie(t, server, "/logout", nil, cookie)
	defer logout.Body.Close()
	if logout.StatusCode != http.StatusSeeOther || logout.Header.Get("Location") != "/login" {
		t.Fatalf("выход = %d, Location = %q", logout.StatusCode, logout.Header.Get("Location"))
	}
}

// TestLoginInvalidCredentials проверяет отказ при неверных данных.
func TestLoginInvalidCredentials(t *testing.T) {
	slice, server := newAuthServer(t, newFakeRequests())
	registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")

	resp := postForm(t, server, "/login", url.Values{"email": {"client@romashka.ru"}, "password": {"wrong"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("неверные данные = %d, ожидался 401", resp.StatusCode)
	}
}

// TestPortalRequiresSession проверяет перенаправление без сессии.
func TestPortalRequiresSession(t *testing.T) {
	server := newServer(t, newFakeRequests())
	for _, path := range []string{"/portal", "/profile", "/portal/request"} {
		resp := getCookie(t, server, path, nil)
		location := resp.Header.Get("Location")
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || location != "/login" {
			t.Fatalf("GET %s = %d, Location = %q, ожидался 303 на /login", path, resp.StatusCode, location)
		}
	}
}

// TestProfileBlockedWhenPending проверяет, что после отправки профиля на
// согласование правка заблокирована.
func TestProfileBlockedWhenPending(t *testing.T) {
	slice, server := newAuthServer(t, newFakeRequests())
	registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	cookie := login(t, server, "client@romashka.ru", "secret")

	form := url.Values{
		"description":     {"Рекрутинговое агентство"},
		"target":          {"IT-компании"},
		"service_name_0":  {"Аутсорсинг найма"},
		"service_short_0": {"Подбор"},
		"service_full_0":  {"Полный цикл подбора"},
		"service_name_1":  {"Скрининг"},
		"service_short_1": {"Отбор резюме"},
		"service_full_1":  {"Первичный отбор резюме"},
	}
	save := postFormCookie(t, server, "/profile/save", form, cookie)
	save.Body.Close()
	if save.StatusCode != http.StatusSeeOther {
		t.Fatalf("сохранение профиля = %d, ожидался 303", save.StatusCode)
	}

	submit := postFormCookie(t, server, "/profile/submit", form, cookie)
	submit.Body.Close()
	if submit.StatusCode != http.StatusSeeOther {
		t.Fatalf("подача профиля = %d, ожидался 303", submit.StatusCode)
	}

	page := body(t, getCookie(t, server, "/profile", cookie))
	if !strings.Contains(page, "правка недоступна") {
		t.Fatalf("профиль не заблокирован:\n%s", page)
	}
	if strings.Contains(page, `action="/profile/save"`) {
		t.Fatalf("форма правки доступна при профиле на согласовании:\n%s", page)
	}
}

// TestPortalShowsServicesAndMetrics проверяет главный экран: услуги и плейсхолдеры
// метрик.
func TestPortalShowsServicesAndMetrics(t *testing.T) {
	slice, server := newAuthServer(t, newFakeRequests())
	tenantID, _ := registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	cookie := login(t, server, "client@romashka.ru", "secret")

	if err := slice.SaveProfile.Handle(context.Background(), command.SaveProfile{
		TenantID:    tenantID,
		Description: "Рекрутинговое агентство",
		Target:      "IT-компании",
		Services:    []domain.Service{{Name: "Аутсорсинг найма", Short: "Подбор", Full: "Полный цикл"}},
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	page := body(t, getCookie(t, server, "/portal", cookie))
	if !strings.Contains(page, "Аутсорсинг найма") {
		t.Fatalf("портал не показал услуги:\n%s", page)
	}
	for _, metric := range []string{"Найдено лидов", "Взаимодействий", "Откликов"} {
		if !strings.Contains(page, metric) {
			t.Fatalf("портал не показал метрику %q:\n%s", metric, page)
		}
	}
}

// TestSubmitRequestThroughSession проверяет подачу заявки через сессию и
// отображение статуса на портале.
func TestSubmitRequestThroughSession(t *testing.T) {
	slice, server := newAuthServer(t, newFakeRequests())
	tenantID, _ := registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	approveDemoProfile(t, slice, tenantID)
	cookie := login(t, server, "client@romashka.ru", "secret")

	resp := postFormCookie(t, server, "/portal/request", url.Values{
		"title":   {"Автоматизация найма"},
		"content": {"Предлагаем автоматизацию найма"},
		"target":  {"IT-компании"},
	}, cookie)
	location := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(location, "/portal") {
		t.Fatalf("подача заявки = %d, Location = %q", resp.StatusCode, location)
	}

	portal := body(t, getCookie(t, server, "/portal", cookie))
	if !strings.Contains(portal, "submitted") {
		t.Fatalf("портал не показал статус заявки:\n%s", portal)
	}
}

// TestPortalShowsFailureReason проверяет, что упавшая заявка на портале
// показывает причину сбоя (ErrorMessage), а не «в работе»: контракт request-intent
// 1.1.0 несёт причину, и портал обязан её отобразить.
func TestPortalShowsFailureReason(t *testing.T) {
	requests := newFakeRequests()
	requests.status = "failed"
	requests.errorReason = "search_failed"
	requests.errorMessage = "поиск упал"

	slice, server := newAuthServer(t, requests)
	tenantID, _ := registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	approveDemoProfile(t, slice, tenantID)
	cookie := login(t, server, "client@romashka.ru", "secret")

	resp := postFormCookie(t, server, "/portal/request", url.Values{
		"title":   {"Автоматизация найма"},
		"content": {"Предлагаем автоматизацию найма"},
		"target":  {"IT-компании"},
	}, cookie)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("подача заявки = %d, ожидался 303", resp.StatusCode)
	}

	portal := body(t, getCookie(t, server, "/portal", cookie))
	for _, want := range []string{"Сбой", "поиск упал", "Причина сбоя"} {
		if !strings.Contains(portal, want) {
			t.Fatalf("портал не показал %q в упавшей заявке:\n%s", want, portal)
		}
	}
	if strings.Contains(portal, "В работе") {
		t.Fatal("упавшая заявка показана как «В работе»")
	}
}
