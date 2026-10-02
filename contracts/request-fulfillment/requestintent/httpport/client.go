// Package httpport — сетевой HTTP-адаптер опубликованного контракта request-intent.
//
// Адаптер реализует requestintent.Port поверх net/http: Submit идёт в
// POST {base}/requests, Status — в GET {base}/requests/{id}?tenant_id=. Он нужен,
// когда потребитель (портал) и владелец заявки (request-fulfillment) запущены
// раздельно и прямой вызов недоступен, а контракт между ними остаётся тем же
// (design.md, Решение 2).
//
// HTTP-коды владельца адаптер переводит в исходы контракта: доменный отказ (400)
// — ErrRejected, отсутствие заявки (404) — ErrNotFound, любой другой код и сбой
// сети — ErrUnavailable. Пакет зависит только от стандартной библиотеки и самого
// контракта и не несёт доменных типов владельца.
package httpport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"lead-gen/contracts/request-fulfillment/requestintent"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер requestintent.Port к сервису request-fulfillment.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ requestintent.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису заявок по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а
// не при первой заявке.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса заявок пуст")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("httpport: разбор базового адреса %q: %w", base, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("httpport: базовый адрес %q должен быть http(s)-URL", base)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("httpport: базовый адрес %q без хоста", base)
	}

	client := &Client{baseURL: base, http: http.DefaultClient}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	if client.http == nil {
		client.http = http.DefaultClient
	}
	return client, nil
}

// Submit реализует requestintent.Port: создаёт заявку владельца командой
// POST {base}/requests. Идентификатор назначает потребитель, поэтому повтор
// команды идемпотентен; в запрос он уходит и телом, и ключом Idempotency-Key.
func (c *Client) Submit(ctx context.Context, in requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
	payload, err := json.Marshal(submitRequest{
		RequestID:     in.RequestID,
		OfferID:       in.OfferID,
		TenantID:      in.TenantID,
		Actor:         in.Actor,
		Content:       in.Content,
		Target:        in.Target,
		CorrelationID: in.CorrelationID,
	})
	if err != nil {
		return requestintent.SubmitResult{}, fmt.Errorf("%w: сборка тела команды: %v", requestintent.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/requests", bytes.NewReader(payload))
	if err != nil {
		return requestintent.SubmitResult{}, fmt.Errorf("%w: сборка запроса: %v", requestintent.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if in.RequestID != "" {
		req.Header.Set("Idempotency-Key", in.RequestID)
	}
	if in.CorrelationID != "" {
		req.Header.Set("X-Correlation-Id", in.CorrelationID)
	}

	resp, err := c.do(req)
	if err != nil {
		return requestintent.SubmitResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return requestintent.SubmitResult{}, errorForStatus(resp)
	}

	var body submitResponse
	if err := decodeBody(resp, &body); err != nil {
		return requestintent.SubmitResult{}, err
	}
	requestID := body.RequestID
	if requestID == "" {
		requestID = in.RequestID
	}
	return requestintent.SubmitResult{RequestID: requestID, Status: body.Status}, nil
}

// Status реализует requestintent.Port: читает состояние заявки запросом
// GET {base}/requests/{id}?tenant_id=. Тенант обязателен: владелец не отличает
// чужую заявку от отсутствующей и отвечает 404.
func (c *Client) Status(ctx context.Context, q requestintent.StatusQuery) (requestintent.StatusResult, error) {
	endpoint := c.baseURL + "/requests/" + url.PathEscape(q.RequestID) + "?tenant_id=" + url.QueryEscape(q.TenantID)
	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodGet, endpoint, nil)
	if err != nil {
		return requestintent.StatusResult{}, fmt.Errorf("%w: сборка запроса: %v", requestintent.ErrUnavailable, err)
	}

	resp, err := c.do(req)
	if err != nil {
		return requestintent.StatusResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return requestintent.StatusResult{}, errorForStatus(resp)
	}

	var body statusResponse
	if err := decodeBody(resp, &body); err != nil {
		return requestintent.StatusResult{}, err
	}
	return requestintent.StatusResult{
		RequestID:     body.RequestID,
		Status:        body.Status,
		OfferContent:  body.Offer.Content,
		OfferTarget:   body.Offer.Target,
		OfferRevision: body.Offer.Revision,
	}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне методов.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", requestintent.ErrUnavailable, err)
	}
	return resp, nil
}

// normalizeContext подставляет фон, если вызов пришёл без контекста: адаптер не
// паникует, но и не выдумывает дедлайн — он остаётся за вызывающим.
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// errorForStatus переводит код неуспешного ответа владельца в исход контракта.
// Доменный отказ и отсутствие заявки различимы; любой другой код, включая 5xx,
// означает недоступность владельца, а не выдуманный исход.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", requestintent.ErrRejected, detail)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", requestintent.ErrNotFound, detail)
	default:
		return fmt.Errorf("%w: %s", requestintent.ErrUnavailable, detail)
	}
}

// errorDetail достаёт сообщение владельца из тела ошибки, не доверяя его
// размеру: тело читается ограниченно.
func errorDetail(resp *http.Response) string {
	fallback := fmt.Sprintf("статус %d", resp.StatusCode)
	if resp.Body == nil {
		return fallback
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	if err != nil || len(data) == 0 {
		return fallback
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && payload.Error != "" {
		return fmt.Sprintf("статус %d: %s", resp.StatusCode, payload.Error)
	}
	return fallback
}

// decodeBody читает полезную нагрузку успешного ответа. Неразборчивый ответ
// владельца — сбой транспорта, а не доменный исход: ErrUnavailable.
func decodeBody(resp *http.Response, dst any) error {
	if resp.Body == nil {
		return fmt.Errorf("%w: пустой ответ владельца", requestintent.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", requestintent.ErrUnavailable, err)
	}
	return nil
}

// submitRequest — тело команды. Имена полей совпадают с контрактом эндпоинта
// request-fulfillment POST /requests.
type submitRequest struct {
	RequestID     string `json:"request_id"`
	OfferID       string `json:"offer_id"`
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	Content       string `json:"content"`
	Target        string `json:"target,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// submitResponse — ответ владельца на команду (201 Created).
type submitResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// statusResponse — ответ владельца на запрос состояния (200 OK). Представление
// владельца не имеет json-тегов, поэтому имена полей — экспортированные имена
// RequestView. Поля оффера — собственные данные клиента; сырых лидов и контактов
// здесь нет по определению контракта.
type statusResponse struct {
	RequestID string `json:"RequestID"`
	Status    string `json:"Status"`
	Offer     struct {
		Content  string `json:"Content"`
		Target   string `json:"Target"`
		Revision int    `json:"Revision"`
	} `json:"Offer"`
}
