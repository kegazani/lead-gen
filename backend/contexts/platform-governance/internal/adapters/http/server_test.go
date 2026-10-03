package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// fakeRequests — управляемый порт контракта request-intent: помнит поданные
// заявки по тенанту и может вернуть сырые поля оффера, чтобы проверить
// маскирование.
type fakeRequests struct {
	mu         sync.Mutex
	byTenant   map[string]string
	rawContent string
	rawTarget  string
	statusErr  error
}

func newFakeRequests() *fakeRequests {
	return &fakeRequests{byTenant: map[string]string{}}
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
		Status:       "submitted",
		OfferContent: f.rawContent,
		OfferTarget:  f.rawTarget,
	}, nil
}

// newServer собирает HTTP-сервер портала поверх заданного контракта.
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

// get выполняет GET-запрос к тестовому серверу.
func get(t *testing.T, server *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := server.Client().Get(server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// postForm отправляет форму и не следует за перенаправлением.
func postForm(t *testing.T, server *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(server.URL+path, form)
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

// TestRootRedirectsToRegister проверяет, что корень сервиса ведёт на регистрацию,
// а не отвечает 404.
func TestRootRedirectsToRegister(t *testing.T) {
	server := newServer(t, newFakeRequests())
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / = %d, ожидался 303", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "/register" {
		t.Fatalf("Location = %q, ожидался /register", location)
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
	resp := get(t, server, "/static/console.css")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("оформление не раздаётся: статус %d", resp.StatusCode)
	}
}

// TestRegisterScreen проверяет рендеринг формы регистрации.
func TestRegisterScreen(t *testing.T) {
	server := newServer(t, newFakeRequests())
	page := body(t, get(t, server, "/register"))
	if !strings.Contains(page, "<!DOCTYPE html>") || !strings.Contains(page, "Регистрация клиента") {
		t.Fatalf("экран регистрации не отрисован: %s", page)
	}
	if !strings.Contains(page, "Описание компании") {
		t.Fatalf("форма не показала поле описания: %s", page)
	}
}

// TestRegisterSubmit проверяет успешную регистрацию: создаётся тенант, клиент
// попадает в портал (POST/redirect/GET).
func TestRegisterSubmit(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := postForm(t, server, "/register", url.Values{
		"company_name": {"ООО Ромашка"},
		"description":  {"Поставщик промышленного оборудования"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("регистрация = %d, ожидался 303: %s", resp.StatusCode, body(t, resp))
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/portal?tenant_id=") {
		t.Fatalf("Location = %q, ожидался переход в портал", location)
	}
}

// TestRegisterIncomplete проверяет неполную регистрацию: экран указывает на
// недостающее поле, а не отдаёт 500.
func TestRegisterIncomplete(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := postForm(t, server, "/register", url.Values{
		"company_name": {"ООО Ромашка"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("неполная регистрация = %d, ожидался 400", resp.StatusCode)
	}
	page := body(t, resp)
	if !strings.Contains(page, "Укажите описание компании") {
		t.Fatalf("экран не указал недостающее поле: %s", page)
	}
}

// TestSubmitRequest проверяет подачу заявки: создаётся заявка submitted,
// привязанная к тенанту, и клиент возвращается на портал.
func TestSubmitRequest(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := postForm(t, server, "/portal/request", url.Values{
		"tenant_id": {"tenant-1"},
		"content":   {"Предлагаем автоматизацию найма"},
		"target":    {"IT-компании"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("подача заявки = %d, ожидался 303: %s", resp.StatusCode, body(t, resp))
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/portal?") || !strings.Contains(location, "tenant_id=tenant-1") || !strings.Contains(location, "request_id=") {
		t.Fatalf("Location = %q, ожидался переход в портал с заявкой", location)
	}

	page := body(t, get(t, server, location))
	if !strings.Contains(page, "submitted") {
		t.Fatalf("портал не показал статус заявки: %s", page)
	}
}

// TestOfferFormScreen проверяет рендеринг формы оффера.
func TestOfferFormScreen(t *testing.T) {
	server := newServer(t, newFakeRequests())
	page := body(t, get(t, server, "/portal/request?tenant_id=tenant-1"))
	if !strings.Contains(page, "Что вы предлагаете") || !strings.Contains(page, "Кому адресован оффер") {
		t.Fatalf("форма оффера не отрисована: %s", page)
	}
}

// TestMasking проверяет, что сырые поля владельца (лиды, контакты) не покидают
// сервер: клиентский ответ содержит только идентификатор и статус заявки.
func TestMasking(t *testing.T) {
	requests := newFakeRequests()
	requests.rawContent = "raw-lead-content company card"
	requests.rawTarget = "contact: lead@example.com"
	server := newServer(t, requests)

	resp := postForm(t, server, "/portal/request", url.Values{
		"tenant_id": {"tenant-1"},
		"content":   {"Оффер"},
	})
	location := resp.Header.Get("Location")
	resp.Body.Close()

	portal := body(t, get(t, server, location))
	if !strings.Contains(portal, "submitted") {
		t.Fatalf("портал не показал статус: %s", portal)
	}
	for _, secret := range []string{requests.rawContent, requests.rawTarget, "lead@example.com"} {
		if strings.Contains(portal, secret) {
			t.Fatalf("сырые данные %q утекли клиенту: %s", secret, portal)
		}
	}

	offer := body(t, get(t, server, "/portal/request?tenant_id=tenant-1"))
	if strings.Contains(offer, requests.rawContent) || strings.Contains(offer, requests.rawTarget) {
		t.Fatalf("сырые данные утекли в форму заявки: %s", offer)
	}
}

// TestTenantIsolation проверяет изоляцию тенантов: чужой тенант не видит заявку
// и не может обратиться к ней по идентификатору.
func TestTenantIsolation(t *testing.T) {
	server := newServer(t, newFakeRequests())
	resp := postForm(t, server, "/portal/request", url.Values{
		"tenant_id": {"tenant-1"},
		"content":   {"Оффер"},
	})
	location := resp.Header.Get("Location")
	resp.Body.Close()

	requestID := ""
	if index := strings.Index(location, "request_id="); index >= 0 {
		requestID = location[index+len("request_id="):]
	}
	if requestID == "" {
		t.Fatalf("не удалось определить идентификатор заявки из %q", location)
	}

	// Чужой тенант без явного идентификатора: заявка не показана.
	other := body(t, get(t, server, "/portal?tenant_id=tenant-2"))
	if strings.Contains(other, requestID) {
		t.Fatalf("портал показал чужую заявку: %s", other)
	}
	if !strings.Contains(other, "не подана") {
		t.Fatalf("портал чужого тенанта должен быть пустым: %s", other)
	}

	// Чужой тенант с явным идентификатором: доступ отклонён (404), существование
	// заявки не раскрывается.
	resp = get(t, server, "/portal?tenant_id=tenant-2&request_id="+requestID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("обращение к чужой заявке = %d, ожидался 404", resp.StatusCode)
	}
}

// TestOwnerUnavailable проверяет, что недоступность владельца заявки видна как
// 502, а не выдаётся за пустой успех.
func TestOwnerUnavailable(t *testing.T) {
	requests := newFakeRequests()
	requests.statusErr = requestintent.ErrUnavailable
	server := newServer(t, requests)

	resp := get(t, server, "/portal?tenant_id=tenant-1&request_id=req-1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("недоступный владелец = %d, ожидался 502", resp.StatusCode)
	}
}
