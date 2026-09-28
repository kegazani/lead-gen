package observ

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Middleware оборачивает HTTP-обработчик наблюдением входящего запроса:
// принимает correlation_id из заголовка либо генерирует новый, кладёт поля
// корреляции в контекст, открывает спан http.request, логирует завершение и
// эмитит RED-метрики запроса. Примитив остаётся без доменных терминов:
// serviceName подставляется при сборке.
func Middleware(logger Logger, metrics Metrics, tracer Tracer, serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			correlationID := strings.TrimSpace(r.Header.Get(correlationIDHeader))
			if correlationID == "" {
				correlationID = NewCorrelationID()
			}
			if len(correlationID) > maxCorrelationIDLen {
				correlationID = correlationID[:maxCorrelationIDLen]
			}

			ctx := WithFields(r.Context(), Fields{CorrelationID: correlationID})
			ctx, finish := tracer.Start(ctx, "http.request")
			r = r.WithContext(ctx)

			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			duration := time.Since(start)

			code := rec.status
			if code == 0 {
				code = http.StatusOK
			}
			codeStr := strconv.Itoa(code)

			metrics.Incr("http_requests_total", map[string]string{"service": serviceName, "code": codeStr})
			metrics.Observe("http_duration_seconds", duration.Seconds(), map[string]string{"service": serviceName})

			logger.Info("запрос обработан",
				Fields{CorrelationID: correlationID},
				slog.String("service", serviceName),
				slog.String("operation", "http.request"),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", code),
				slog.Duration("duration", duration),
			)

			if code >= 500 {
				finish(fmt.Errorf("observ: статус ответа %d", code))
			} else {
				finish(nil)
			}
		})
	}
}

// statusRecorder — обёртка ResponseWriter, запоминающая код статуса ответа,
// чтобы лог и метрика несли исход запроса.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap возвращает исходный ResponseWriter, чтобы http.ResponseController и
// адаптеры возможностей (Flush, Hijack, ...) находили их у нижележащего писателя.
// Без этого потоковый ответ (text/event-stream) не сбрасывается по мере генерации
// и рвётся после первого кадра.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Flush пробрасывает сброс буфера, если его поддерживает нижележащий писатель:
// потоковые ответы должны доходить до клиента до завершения обработчика.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
