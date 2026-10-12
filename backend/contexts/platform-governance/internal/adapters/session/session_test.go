package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestManagerCreateLookupDelete проверяет жизненный цикл сессии: создание,
// чтение с продлением активности и удаление.
func TestManagerCreateLookupDelete(t *testing.T) {
	manager := NewManager(nil, nil)
	ctx := context.Background()

	id, err := manager.Create(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("идентификатор сессии пуст")
	}

	sess, ok := manager.Lookup(ctx, id)
	if !ok || sess.TenantID() != "tenant-1" || sess.UserID() != "user-1" {
		t.Fatalf("Lookup = %+v, %v", sess, ok)
	}

	manager.Delete(ctx, id)
	if _, ok := manager.Lookup(ctx, id); ok {
		t.Fatal("сессия не удалена")
	}
}

// TestManagerExpiresSession проверяет истечение сессии по простою.
func TestManagerExpiresSession(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	manager := NewManager(nil, func() time.Time { return now })
	ctx := context.Background()

	id, err := manager.Create(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Сдвиг времени за пределы простоя.
	now = now.Add(DefaultIdleTimeout + time.Hour)
	if _, ok := manager.Lookup(ctx, id); ok {
		t.Fatal("истёкшая сессия не отвергнута")
	}
}

// TestMiddlewareRedirects проверяет перенаправление без валидной сессии и
// передачу сессии в контекст при валидной.
func TestMiddlewareRedirects(t *testing.T) {
	manager := NewManager(nil, nil)
	ctx := context.Background()
	id, err := manager.Create(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	handler := manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := FromContext(r.Context())
		if sess == nil {
			t.Fatal("сессия не попала в контекст")
		}
		if sess.TenantID() != "tenant-1" {
			t.Fatalf("тенант = %q", sess.TenantID())
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Без cookie — редирект на /login.
	req := httptest.NewRequest(http.MethodGet, "/portal", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("без сессии = %d, Location = %q", rec.Code, rec.Header().Get("Location"))
	}

	// С валидной cookie — обработчик выполняется.
	req = httptest.NewRequest(http.MethodGet, "/portal", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: id})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("с сессией = %d", rec.Code)
	}
}
