package csrf

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestVerifyRejectsMissing проверяет, что без токена и cookie изменяющий запрос
// отвергается.
func TestVerifyRejectsMissing(t *testing.T) {
	guard := NewGuard()
	if guard.Verify(httptest.NewRequest(http.MethodPost, "/x", nil)) {
		t.Fatal("запрос без токена признан валидным")
	}
}

// TestDoubleSubmit проверяет базовый сценарий: выданный токен проходит, чужой —
// нет.
func TestDoubleSubmit(t *testing.T) {
	guard := NewGuard()
	recorder := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/form", nil)
	token := guard.EnsureToken(recorder, getReq)
	if token == "" {
		t.Fatal("токен не выдан")
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("cookie с токеном не поставлена")
	}

	post := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(url.Values{"csrf": {token}}.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(cookies[0])
	if err := post.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	if !guard.Verify(post) {
		t.Fatal("выданный токен не прошёл проверку")
	}

	tampered := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(url.Values{"csrf": {"deadbeef"}}.Encode()))
	tampered.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tampered.AddCookie(cookies[0])
	if err := tampered.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	if guard.Verify(tampered) {
		t.Fatal("подделанный токен прошёл проверку")
	}
}

// TestVerifyUsesHeader проверяет, что токен можно передать заголовком.
func TestVerifyUsesHeader(t *testing.T) {
	guard := NewGuard()
	recorder := httptest.NewRecorder()
	token := guard.EnsureToken(recorder, httptest.NewRequest(http.MethodGet, "/form", nil))
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("cookie с токеном не поставлена")
	}
	req := httptest.NewRequest(http.MethodPost, "/submit", nil)
	req.AddCookie(cookies[0])
	req.Header.Set("X-CSRF-Token", token)
	if !guard.Verify(req) {
		t.Fatal("токен из заголовка не прошёл проверку")
	}
}
