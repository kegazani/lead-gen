package components

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJoinPrefix(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{name: "пустой префикс возвращает путь", base: "", path: "/jobs", want: "/jobs"},
		{name: "корень даёт префикс со слэшем", base: "/leads", path: "/", want: "/leads/"},
		{name: "пустой путь даёт префикс со слэшем", base: "/leads", path: "", want: "/leads/"},
		{name: "склейка префикса и пути", base: "/leads", path: "/jobs", want: "/leads/jobs"},
		{name: "путь без ведущего слэша", base: "/leads", path: "jobs", want: "/leads/jobs"},
		{name: "завершающий слэш префикса снимается", base: "/leads/", path: "/jobs", want: "/leads/jobs"},
		{name: "префикс без ведущего слэша", base: "leads", path: "/jobs", want: "/leads/jobs"},
		{name: "корневой префикс не добавляется", base: "/", path: "/jobs", want: "/jobs"},
		{name: "пустой путь без префикса", base: "", path: "", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JoinPrefix(tt.base, tt.path); got != tt.want {
				t.Fatalf("JoinPrefix(%q, %q) = %q, ожидался %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}

func TestPrefixLocalURL(t *testing.T) {
	tests := []struct {
		name string
		u    string
		want string
	}{
		{name: "локальный путь", u: "/jobs", want: "/leads/jobs"},
		{name: "корень", u: "/", want: "/leads/"},
		{name: "внешний URL", u: "https://example.test/x", want: "https://example.test/x"},
		{name: "scheme-relative", u: "//example.test/x", want: "//example.test/x"},
		{name: "относительный", u: "jobs", want: "jobs"},
		{name: "якорь", u: "#top", want: "#top"},
		{name: "пустой", u: "", want: ""},
		{name: "префикс другого сервиса", u: "/requests/validation", want: "/requests/validation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			externals := []string{"/requests", "/reports"}
			if got := prefixLocalURL("/leads", tt.u, externals); got != tt.want {
				t.Fatalf("prefixLocalURL(%q) = %q, ожидался %q", tt.u, got, tt.want)
			}
		})
	}
}

func TestPrefixMiddlewareStripsPrefix(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	handler := PrefixMiddleware("/leads", next)

	req := httptest.NewRequest(http.MethodGet, "/leads/jobs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if seen != "/jobs" {
		t.Fatalf("путь после снятия префикса = %q, ожидался /jobs", seen)
	}
}

func TestPrefixMiddlewareRoot(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	handler := PrefixMiddleware("/leads", next)

	req := httptest.NewRequest(http.MethodGet, "/leads", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if seen != "/" {
		t.Fatalf("путь после снятия префикса = %q, ожидался /", seen)
	}
}

// TestPrefixMiddlewareIntakeDoesNotStripContractRoutes защищает от коллизии
// префикса /requests с контрактом request-intent: при BasePath=/intake прямой
// межсервисный вызов POST /requests (пер-сервисный порт, без прокси) не должен
// срезаться middleware, тогда как консольный путь под префиксом (/intake/...) —
// должен срезаться до корневого маршрута.
func TestPrefixMiddlewareIntakeDoesNotStripContractRoutes(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	handler := PrefixMiddleware("/intake", next)

	// Консольный экран под префиксом срезается до /validation.
	req := httptest.NewRequest(http.MethodGet, "/intake/validation", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if seen != "/validation" {
		t.Fatalf("консольный /intake/validation после снятия префикса = %q, ожидался /validation", seen)
	}

	// Контрактный POST /requests не срезается (не лежит под /intake).
	req = httptest.NewRequest(http.MethodPost, "/requests", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if seen != "/requests" {
		t.Fatalf("контрактный POST /requests после снятия префикса = %q, ожидался /requests", seen)
	}
}

func TestPrefixMiddlewareRewritesLocation(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
	handler := PrefixMiddleware("/leads", next)

	req := httptest.NewRequest(http.MethodGet, "/leads/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); got != "/leads/login" {
		t.Fatalf("Location = %q, ожидался /leads/login", got)
	}
}

func TestPrefixMiddlewareRewritesHTML(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><head><title>t</title></head><body>
<a href="/cards/1">c</a>
<a href="/requests/validation">req</a>
<form action="/search" method="post"></form>
<div hx-get="/jobs" hx-trigger="every 2s"></div>
<script src="/static/console.js"></script>
<a href="https://ext.example/x">ext</a>
</body></html>`))
	})
	handler := PrefixMiddleware("/leads", next, "/requests", "/reports")

	req := httptest.NewRequest(http.MethodGet, "/leads/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		`href="/leads/cards/1"`,
		`action="/leads/search"`,
		`hx-get="/leads/jobs"`,
		`src="/leads/static/console.js"`,
		`<meta name="admin-base-path" content="/leads">`,
		`href="https://ext.example/x"`,
		// Внешний префикс другого сервиса не переписывается.
		`href="/requests/validation"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("тело не содержит %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `href="/leads/requests/validation"`) {
		t.Fatalf("ссылка на другой сервис переписана:\n%s", body)
	}
}

func TestPrefixMiddlewareLeavesNonHTML(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: /portal/stream\n\n"))
	})
	handler := PrefixMiddleware("/admin", next)

	req := httptest.NewRequest(http.MethodGet, "/admin/portal/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Body.String(); got != "data: /portal/stream\n\n" {
		t.Fatalf("SSE-тело изменено: %q", got)
	}
}

func TestPrefixMiddlewareLeavesAttachment(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="leads.html"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<a href="/cards/1">c</a>`))
	})
	handler := PrefixMiddleware("/leads", next)

	req := httptest.NewRequest(http.MethodGet, "/leads/leads/export", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Body.String(); got != `<a href="/cards/1">c</a>` {
		t.Fatalf("скачиваемый документ переписан: %q", got)
	}
}

func TestPrefixMiddlewareEmptyBasePassthrough(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := PrefixMiddleware("", next)
	req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !called {
		t.Fatal("пустой префикс должен вызывать следующий обработчик без изменений")
	}
}

func TestPrefixMiddlewarePassesThroughNonPrefixedPath(t *testing.T) {
	// Запрос, не лежащий под префиксом (например, клиентский портал сервиса,
	// смонтированный без префикса), не должен переписываться.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<a href="/portal/request">p</a>`))
	})
	handler := PrefixMiddleware("/admin", next)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Body.String(); got != `<a href="/portal/request">p</a>` {
		t.Fatalf("ответ портала переписан: %q", got)
	}
}
