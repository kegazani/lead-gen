package components

import "net/http"

// SecurityHeaders навешивает заголовки безопасности до записи тела ответа.
// Политика Content-Security-Policy задаётся потребителем: у публичного edge и
// у консоли оператора разные источники и разные разрешения, поэтому общий
// компонент фиксирует только способ установки, а не саму политику.
func SecurityHeaders(policy string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			if policy != "" {
				header.Set("Content-Security-Policy", policy)
			}
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
		})
	}
}
