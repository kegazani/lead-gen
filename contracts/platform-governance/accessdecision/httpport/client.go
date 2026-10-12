// Package httpport — сетевой HTTP-адаптер синхронного решения контракта
// access-decision.
//
// Владелец (`platform-governance`) и потребитель (`outreach-orchestration`,
// `data-governance`) могут исполняться раздельно; тогда решение получается
// сетевым вызовом POST {base}{PathCanAct}. Пакет зависит только от стандартной
// библиотеки и самого контракта и не несёт доменных типов владельца.
//
// Недоступность владельца — честный отказ: сбой сети, не-2xx код и неразборчивый
// ответ переводятся в accessdecision.ErrUnavailable и никогда в положительное
// решение. Форма контракта (путь и DTO) объявлена в самом контракте, поэтому
// владелец и клиент не расходятся.
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
	"time"

	"lead-gen/contracts/platform-governance/accessdecision"
)

const (
	// defaultTimeout — бюджет сетевого вызова по умолчанию. Синхронный вызов без
	// границы недопустим; дедлайн вызывающего соблюдается через context.
	defaultTimeout = 5 * time.Second

	// maxBodyBytes ограничивает размер разбираемого ответа владельца.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер accessdecision.Decider к сервису platform-governance.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ accessdecision.Decider = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает клиент по умолчанию с бюджетом вызова.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису-владельцу по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первом решении.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес владельца решения пуст")
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

	client := &Client{baseURL: base, http: &http.Client{Timeout: defaultTimeout}}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	if client.http == nil {
		client.http = &http.Client{Timeout: defaultTimeout}
	}
	return client, nil
}

// CanAct реализует accessdecision.Decider: запрашивает решение владельца командой
// POST {base}{PathCanAct}. Ошибка (в том числе недоступность владельца) не
// превращается в положительное решение.
func (c *Client) CanAct(ctx context.Context, actor, tenant string) (bool, error) {
	payload, err := json.Marshal(accessdecision.CanActRequest{Actor: actor, Tenant: tenant})
	if err != nil {
		return false, fmt.Errorf("%w: сборка тела запроса: %v", accessdecision.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+accessdecision.PathCanAct, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("%w: сборка запроса: %v", accessdecision.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %v", accessdecision.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("%w: %s", accessdecision.ErrUnavailable, errorDetail(resp))
	}

	var body accessdecision.CanActResponse
	if resp.Body == nil {
		return false, fmt.Errorf("%w: пустой ответ владельца", accessdecision.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return false, fmt.Errorf("%w: разбор ответа владельца: %v", accessdecision.ErrUnavailable, err)
	}
	return body.Allowed, nil
}

// normalizeContext подставляет фон, если вызов пришёл без контекста: адаптер не
// паникует, но и не выдумывает дедлайн — он остаётся за вызывающим.
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// errorDetail достаёт сообщение владельца из тела ошибки, не доверяя его размеру:
// тело читается ограниченно.
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
