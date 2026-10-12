// Package httpport — сетевой HTTP-адаптер опубликованного контракта campaign-report.
//
// Адаптер реализует campaignreport.Port поверх net/http: Numbers читает
// GET {base}/operations/numbers, Clients — GET {base}/operations/clients. Он
// нужен, когда потребитель (экран операций platform-governance) и владелец
// (analytics) запущены раздельно и прямой вызов недоступен, а контракт между
// ними остаётся тем же (design.md, Решение 6).
//
// HTTP-коды владельца адаптер переводит в исходы контракта: 400 — ErrRejected,
// 503 и любой другой неуспешный код, как и сбой сети, — ErrUnavailable. Пустой
// список не подменяет недоступность владельца. Пакет зависит только от
// стандартной библиотеки и самого контракта и не несёт доменных типов владельца.
package httpport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lead-gen/contracts/analytics/campaignreport"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер campaignreport.Port к сервису analytics. Безопасен
// для одновременного использования.
type Client struct {
	baseURL string
	actor   string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ campaignreport.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// WithActor задаёт актора запроса (для трассировки отказа владельцем).
func WithActor(actor string) Option {
	return func(cl *Client) { cl.actor = strings.TrimSpace(actor) }
}

// New собирает сетевой адаптер к сервису analytics по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первом чтении.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("campaignreport/httpport: базовый адрес сервиса пуст")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("campaignreport/httpport: разбор базового адреса %q: %w", base, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("campaignreport/httpport: базовый адрес %q должен быть http(s)-URL", base)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("campaignreport/httpport: базовый адрес %q без хоста", base)
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

// Numbers реализует campaignreport.Port: читает цифры процесса командой
// GET {base}/operations/numbers.
func (c *Client) Numbers(ctx context.Context, request campaignreport.Request) (campaignreport.NumbersResponse, error) {
	var response campaignreport.NumbersResponse
	if err := c.get(ctx, "/operations/numbers", request, &response); err != nil {
		return campaignreport.NumbersResponse{}, err
	}
	response.Clients = normalize(response.Clients)
	response.Periods = normalize(response.Periods)
	return response, nil
}

// Clients реализует campaignreport.Port: читает агрегаты по клиентам командой
// GET {base}/operations/clients.
func (c *Client) Clients(ctx context.Context, request campaignreport.Request) (campaignreport.ClientsResponse, error) {
	var response campaignreport.ClientsResponse
	if err := c.get(ctx, "/operations/clients", request, &response); err != nil {
		return campaignreport.ClientsResponse{}, err
	}
	response.Clients = normalize(response.Clients)
	return response, nil
}

// get выполняет GET-запрос, соблюдая контекст, и разбирает тело успешного ответа.
func (c *Client) get(ctx context.Context, path string, request campaignreport.Request, dst any) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: %v", campaignreport.ErrRejected, err)
	}
	query := url.Values{}
	if request.Window > 0 {
		query.Set("window_seconds", strconv.FormatInt(int64(request.Window/time.Second), 10))
	}
	actor := strings.TrimSpace(request.Actor)
	if actor == "" {
		actor = c.actor
	}
	if actor != "" {
		query.Set("actor", actor)
	}
	target := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%w: сборка запроса: %v", campaignreport.ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", campaignreport.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errorForStatus(resp)
	}
	if resp.Body == nil {
		return fmt.Errorf("%w: пустой ответ владельца", campaignreport.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", campaignreport.ErrUnavailable, err)
	}
	return nil
}

// normalizeContext подставляет фон, если вызов пришёл без контекста.
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// normalize гарантирует не-nil срез: пустой список — корректный результат,
// отличимый от недоступности.
func normalize[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// errorForStatus переводит код неуспешного ответа владельца в исход контракта.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", campaignreport.ErrRejected, detail)
	default:
		return fmt.Errorf("%w: %s", campaignreport.ErrUnavailable, detail)
	}
}

// errorDetail достаёт сообщение владельца из тела ошибки, не доверяя его размеру.
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
