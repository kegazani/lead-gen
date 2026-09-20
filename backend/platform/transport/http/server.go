package transport

import (
	"context"
	"io"
	"net/http"
)

// NewServer собирает HTTP-обработчик владельца ресурса. Он восстанавливает
// Request из запроса (тело, конверт из заголовков, ключ идемпотентности),
// передаёт его handle и переносит конверт ответа обратно в заголовки.
//
// Обработчик не принимает доменных решений: идемпотентность и проверка
// утверждения выполняются в handle, поэтому транспорт не может обойти
// утверждение и не создаёт эффектов сам.
func NewServer(handle func(ctx context.Context, req Request) (Response, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "transport/http: чтение тела запроса", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		req := Request{
			Method:         r.Method,
			URL:            r.URL.String(),
			Body:           body,
			Envelope:       readEnvelope(r.Header),
			IdempotencyKey: r.Header.Get(headerIdempotencyKey),
		}

		resp, err := handle(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if resp.StatusCode == 0 {
			resp.StatusCode = http.StatusOK
		}
		writeEnvelope(w.Header(), resp.Envelope)
		if len(resp.Body) > 0 {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(resp.StatusCode)
		if len(resp.Body) > 0 {
			_, _ = w.Write(resp.Body)
		}
	})
}
