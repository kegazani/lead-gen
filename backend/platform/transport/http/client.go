package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"lead-gen/backend/platform/contracts"
	"lead-gen/backend/platform/reliability/resilience"
)

// Request — запрос синхронного вызова.
type Request struct {
	// Method — HTTP-метод: POST для command, GET для query.
	Method string
	// URL — адрес ресурса владельца.
	URL string
	// Body — тело запроса (JSON), непрозрачное для транспорта.
	Body []byte
	// Envelope — конверт контракта: корреляция, тенант, версия схемы.
	Envelope contracts.Envelope
	// IdempotencyKey — стабильный ключ идемпотентности command. Пустой ключ
	// делает command неидемпотентным: такой вызов не повторяется.
	IdempotencyKey string
	// Timeout — собственный бюджет вызова. Ноль означает бюджет контекста.
	Timeout time.Duration
}

// Response — ответ синхронного вызова.
type Response struct {
	// StatusCode — код ответа владельца.
	StatusCode int
	// Body — тело ответа (JSON), непрозрачное для транспорта.
	Body []byte
	// Envelope — конверт ответа.
	Envelope contracts.Envelope
}

// Client — клиент синхронного вызова по контрактам владельца.
type Client interface {
	Do(ctx context.Context, req Request) (Response, error)
}

// client — реализация Client.
type client struct {
	rt      http.RoundTripper
	policy  resilience.RetryPolicy
	breaker resilience.Breaker
}

// NewClient собирает клиент. Нулевой RoundTripper заменяется на
// http.DefaultTransport, нулевой предохранитель означает «не ограничивать».
func NewClient(rt http.RoundTripper, policy resilience.RetryPolicy, breaker resilience.Breaker) Client {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &client{rt: rt, policy: policy, breaker: breaker}
}

// Do выполняет вызов в собственном бюджете: повтор разрешён только для
// идемпотентных операций (query и command со стабильным ключом), а серия
// отказов downstream размыкает предохранитель и отклоняет вызов явно.
//
// Ответ 5xx и сетевые ошибки считаются отказом и участвуют в ретраях, ответ 4xx
// возвращается вызывающему как есть: повтор не исправит ошибку запроса.
func (c *client) Do(ctx context.Context, req Request) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.breaker != nil && !c.breaker.Allow() {
		return Response{}, fmt.Errorf("transport/http: предохранитель разомкнут, вызов %s %s отклонён", req.Method, req.URL)
	}

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	var response Response
	err := c.policy.Do(ctx, isIdempotent(req), func(ctx context.Context) error {
		resp, err := c.roundTrip(ctx, req)
		if err != nil {
			return err
		}
		response = resp
		return nil
	})
	if c.breaker != nil {
		c.breaker.Record(err == nil)
	}
	if err != nil {
		return Response{}, err
	}
	return response, nil
}

// roundTrip выполняет одну попытку вызова.
func (c *client) roundTrip(ctx context.Context, req Request) (Response, error) {
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return Response{}, fmt.Errorf("transport/http: сборка запроса %s %s: %w", req.Method, req.URL, err)
	}
	writeEnvelope(httpReq.Header, req.Envelope)
	if req.IdempotencyKey != "" {
		httpReq.Header.Set(headerIdempotencyKey, req.IdempotencyKey)
	}
	if len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	httpResp, err := c.rt.RoundTrip(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("transport/http: вызов %s %s: %w", req.Method, req.URL, err)
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("transport/http: чтение ответа %s %s: %w", req.Method, req.URL, err)
	}
	if httpResp.StatusCode >= http.StatusInternalServerError {
		return Response{}, fmt.Errorf("transport/http: %s %s вернул статус %d", req.Method, req.URL, httpResp.StatusCode)
	}
	return Response{
		StatusCode: httpResp.StatusCode,
		Body:       data,
		Envelope:   readEnvelope(httpResp.Header),
	}, nil
}

// isIdempotent определяет, допустим ли повтор: query (GET) идемпотентен;
// command идемпотентен только при стабильном ключе идемпотентности.
func isIdempotent(req Request) bool {
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return true
	}
	return req.Method == http.MethodPost && req.IdempotencyKey != ""
}
