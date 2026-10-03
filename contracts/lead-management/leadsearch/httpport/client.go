// Package httpport — сетевой HTTP-адаптер опубликованного контракта lead-search.
//
// Адаптер реализует leadsearch.Port поверх net/http: Search идёт в
// POST {base}/lead-search. Он нужен, когда потребитель (request-fulfillment) и
// владелец поиска (lead-management) запущены раздельно и прямой вызов
// недоступен, а контракт между ними остаётся тем же (design.md, Решение 2).
//
// HTTP-коды владельца адаптер переводит в исходы контракта: доменный отказ (400)
// — ErrRejected, любой другой код и сбой сети — ErrUnavailable. Пакет зависит
// только от стандартной библиотеки и самого контракта и не несёт доменных типов
// владельца.
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

	"lead-gen/contracts/lead-management/leadsearch"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер leadsearch.Port к сервису lead-management. Безопасен
// для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ leadsearch.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису поиска по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первом поиске.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса поиска пуст")
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

// Search реализует leadsearch.Port: ищет лидов командой POST {base}/lead-search.
func (c *Client) Search(ctx context.Context, query leadsearch.Query) (leadsearch.Result, error) {
	payload, err := json.Marshal(searchRequest{
		RequestID: query.RequestID,
		TenantID:  query.TenantID,
		Criteria:  query.Criteria,
		Limit:     query.Limit,
	})
	if err != nil {
		return leadsearch.Result{}, fmt.Errorf("%w: сборка тела команды: %v", leadsearch.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/lead-search", bytes.NewReader(payload))
	if err != nil {
		return leadsearch.Result{}, fmt.Errorf("%w: сборка запроса: %v", leadsearch.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return leadsearch.Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return leadsearch.Result{}, errorForStatus(resp)
	}

	var body searchResponse
	if err := decodeBody(resp, &body); err != nil {
		return leadsearch.Result{}, err
	}
	leads := make([]leadsearch.LeadRef, 0, len(body.Leads))
	for _, lead := range body.Leads {
		leads = append(leads, leadsearch.LeadRef{ID: lead.ID, Origin: lead.Origin})
	}
	return leadsearch.Result{
		Found:    body.Found,
		FromPool: body.FromPool,
		Crawled:  body.Crawled,
		Leads:    leads,
	}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне Search.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", leadsearch.ErrUnavailable, err)
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
// Доменный отказ различим; любой другой код, включая 5xx, означает недоступность
// владельца, а не выдуманный пустой результат.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: %s", leadsearch.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", leadsearch.ErrUnavailable, detail)
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
		return fmt.Errorf("%w: пустой ответ владельца", leadsearch.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", leadsearch.ErrUnavailable, err)
	}
	return nil
}

// searchRequest — тело команды. Имена полей совпадают с контрактом эндпоинта
// lead-management POST /lead-search.
type searchRequest struct {
	RequestID string `json:"request_id"`
	TenantID  string `json:"tenant_id"`
	Criteria  string `json:"criteria"`
	Limit     int    `json:"limit"`
}

// leadRefWire — ссылка на найденный лид в ответе владельца.
type leadRefWire struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
}

// searchResponse — ответ владельца на команду поиска (200 OK).
type searchResponse struct {
	Found    int           `json:"found"`
	FromPool int           `json:"from_pool"`
	Crawled  int           `json:"crawled"`
	Leads    []leadRefWire `json:"leads"`
}
