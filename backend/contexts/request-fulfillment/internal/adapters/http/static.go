package httpapi

import (
	"embed"
	"net/http"
)

// staticFS — статические ресурсы экранов заявок, встроенные в бинарник. CSP
// консоли запрещает inline-скрипты, поэтому клиентская подписка на SSE-поток —
// отдельный файл, а не вставка в разметку.
//
//go:embed static/*.js
var staticFS embed.FS

// companyStreamScript раздаёт скрипт клиентской подписки на поток статусов.
// Файл встроен: разметка экрана не может разойтись со скриптом, который её
// оживляет.
func (s *Server) companyStreamScript() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/company_requests.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(data)
	}
}
