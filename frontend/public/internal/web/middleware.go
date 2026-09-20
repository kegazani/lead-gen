package web

import "net/http"

// contentSecurityPolicy публичных страниц: скриптов нет вовсе, оформление —
// внешний файл, форма отправляется только на свой домен. Политика задана
// белым списком, а не запретом частного случая. Сам механизм установки
// заголовков — общий компонент (frontend/components), политика — edge.
const contentSecurityPolicy = "default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// logRequests пишет в журнал маршрут, код ответа и длительность. Значения
// полей запроса и тела форм не журналируются: заявка посетителя — персональные
// данные, а журнал — не место для них (architecture.md, §10).
func (h *handler) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := h.now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		h.log.Info("web: запрос",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration", h.now().Sub(started),
		)
	})
}

// recoverPanic не даёт панике одного обработчика уронить процесс edge:
// публичный контур обслуживает много страниц, и отказ одной не должен
// закрывать остальные. Наружу уходит общий отказ без подробностей.
func (h *handler) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				h.log.Error("web: паника обработчика",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", recovered,
				)
				http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder запоминает код ответа для журнала. Значение по умолчанию —
// 200: обработчик, который пишет тело без WriteHeader, отвечает успехом.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader записывает код один раз: повторный вызов после записи тела не
// доходит до стандартного ответа, иначе он ругался бы на лишний WriteHeader.
func (r *statusRecorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.status = status
	r.wrote = true
	r.ResponseWriter.WriteHeader(status)
}

// Write отмечает начало тела ответа.
func (r *statusRecorder) Write(data []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(data)
}
