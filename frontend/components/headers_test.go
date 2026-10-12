package components

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNoStoreSetsCacheControl проверяет критерий задачи 7.5: ответы с
// персональными данными несут Cache-Control: no-store.
func TestNoStoreSetsCacheControl(t *testing.T) {
	handler := NoStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("персональные данные"))
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	cacheControl := response.Header().Get("Cache-Control")
	if !containsDirective(cacheControl, "no-store") {
		t.Fatalf("Cache-Control = %q, ожидалась директива no-store", cacheControl)
	}
	if response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("Pragma = %q, ожидалось no-cache", response.Header().Get("Pragma"))
	}
}

// TestSetNoStoreAppliesToCurrentResponse проверяет, что обработчик может выставить
// запрет кэширования сам, не оборачиваясь middleware.
func TestSetNoStoreAppliesToCurrentResponse(t *testing.T) {
	response := httptest.NewRecorder()
	SetNoStore(response)
	if !containsDirective(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("SetNoStore не выставил no-store: %q", response.Header().Get("Cache-Control"))
	}
}

// containsDirective ищет директиву в списке через запятую.
func containsDirective(header, directive string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == directive {
			return true
		}
	}
	return false
}
