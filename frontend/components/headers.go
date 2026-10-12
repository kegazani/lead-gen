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

// NoStore запрещает кэширование ответа: страницы и фрагменты с персональными
// данными не должны оседать в кэшах браузера и посредников
// (web-frontend-architecture, срез 7, 7.5; design.md, Решение 15). Заголовки
// ставятся до записи тела.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetNoStore(w)
		next.ServeHTTP(w, r)
	})
}

// SetNoStore проставляет заголовки запрета кэширования на уже выбранный ответ.
// Нужен обработчикам, которые возвращают персональные данные фрагментом и
// управляют заголовками сами.
func SetNoStore(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
}
