package components

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRateLimitBlocksAboveThreshold проверяет критерий задачи 7.5: превышение
// порога чувствительных действий блокируется (429) и фиксируется (OnBlocked).
func TestRateLimitBlocksAboveThreshold(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	blocked := 0
	handler := RateLimit(RateLimitConfig{
		Limit:     3,
		Window:    time.Minute,
		KeyFunc:   func(*http.Request) string { return "operator" },
		Now:       func() time.Time { return now },
		OnBlocked: func(*http.Request, string) { blocked++ },
	})(okHandler())

	for attempt := 1; attempt <= 3; attempt++ {
		if status := serve(handler); status != http.StatusOK {
			t.Fatalf("запрос %d = %d, ожидался 200", attempt, status)
		}
	}
	if status := serve(handler); status != http.StatusTooManyRequests {
		t.Fatalf("превышение порога = %d, ожидался 429", status)
	}
	if blocked != 1 {
		t.Fatalf("факт блокировки зафиксирован %d раз, ожидался 1", blocked)
	}
}

// TestRateLimitWindowResets проверяет, что окно истекает и запросы снова
// проходят: блокировка не вечная.
func TestRateLimitWindowResets(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	handler := RateLimit(RateLimitConfig{
		Limit:   1,
		Window:  time.Minute,
		KeyFunc: func(*http.Request) string { return "operator" },
		Now:     func() time.Time { return now },
	})(okHandler())

	if status := serve(handler); status != http.StatusOK {
		t.Fatalf("первый запрос = %d, ожидался 200", status)
	}
	if status := serve(handler); status != http.StatusTooManyRequests {
		t.Fatalf("второй запрос = %d, ожидался 429", status)
	}
	now = now.Add(time.Minute + time.Second)
	if status := serve(handler); status != http.StatusOK {
		t.Fatalf("запрос после окна = %d, ожидался 200", status)
	}
}

// TestRateLimitIsolatesKeys проверяет, что порог считается по ключу: блокировка
// одного ключа не задевает другой, и пустой ключ не ограничивается.
func TestRateLimitIsolatesKeys(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	key := "first"
	handler := RateLimit(RateLimitConfig{
		Limit:   1,
		Window:  time.Minute,
		KeyFunc: func(*http.Request) string { return key },
		Now:     func() time.Time { return now },
	})(okHandler())

	if status := serve(handler); status != http.StatusOK {
		t.Fatalf("ключ first: первый запрос = %d", status)
	}
	if status := serve(handler); status != http.StatusTooManyRequests {
		t.Fatalf("ключ first: второй запрос = %d, ожидался 429", status)
	}
	key = "second"
	if status := serve(handler); status != http.StatusOK {
		t.Fatalf("ключ second не должен блокироваться первым: %d", status)
	}
	key = ""
	if status := serve(handler); status != http.StatusOK {
		t.Fatalf("пустой ключ не ограничивается: %d", status)
	}
}

// TestRateLimitDefaultKeyUsesClientAddress проверяет ключ по умолчанию: адрес
// клиента без порта.
func TestRateLimitDefaultKeyUsesClientAddress(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "203.0.113.7:54321"
	if got := clientKey(request); got != "203.0.113.7" {
		t.Fatalf("clientKey = %q, ожидался адрес без порта", got)
	}
}

// okHandler — успешный обработчик для тестов ограничителя.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// serve выполняет запрос через обработчик и возвращает код ответа.
func serve(handler http.Handler) int {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	return response.Code
}
