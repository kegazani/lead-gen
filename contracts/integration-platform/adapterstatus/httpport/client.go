// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// adapter-status.
//
// Адаптер реализует adapterstatus.Port поверх net/http: Statuses идёт в
// GET {base}/adapter-status. Он нужен, когда потребитель (platform-governance) и
// владелец (integration-platform) запущены раздельно и прямой вызов недоступен, а
// контракт между ними остаётся тем же.
//
// Любой код, кроме 200, и сбой сети переводятся в adapterstatus.ErrUnavailable:
// пустой список не подменяет недоступность владельца. Пакет зависит только от
// стандартной библиотеки и самого контракта и не несёт доменных типов владельца.
package httpport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"lead-gen/contracts/integration-platform/adapterstatus"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер adapterstatus.Port к сервису integration-platform.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ adapterstatus.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер по базовому адресу. Базовый адрес обязан быть
// абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не при первом
// запросе.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес integration-platform пуст")
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

// Statuses реализует adapterstatus.Port: читает состояние адаптеров командой
// GET {base}/adapter-status.
func (c *Client) Statuses(ctx context.Context) ([]adapterstatus.Status, error) {
	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodGet, c.baseURL+"/adapter-status", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: сборка запроса: %v", adapterstatus.ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", adapterstatus.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: владелец ответил %d: %s", adapterstatus.ErrUnavailable, resp.StatusCode, errorDetail(resp))
	}

	var body adapterstatus.Response
	if err := decodeBody(resp, &body); err != nil {
		return nil, err
	}
	if body.Adapters == nil {
		body.Adapters = []adapterstatus.Status{}
	}
	return body.Adapters, nil
}

// normalizeContext подставляет фон, если вызов пришёл без контекста: адаптер не
// паникует и не выдумывает дедлайн — он остаётся за вызывающим.
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// decodeBody разбирает тело ответа владельца, переводя сбой разбора в
// недоступность.
func decodeBody(resp *http.Response, into *adapterstatus.Response) error {
	limited := io.LimitReader(resp.Body, maxBodyBytes)
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("%w: разбор ответа: %v", adapterstatus.ErrUnavailable, err)
	}
	return nil
}

// errorDetail читает ограниченный фрагмент тела ошибки, обрезая управляющие
// символы, чтобы сбойный ответ не раздувал текст.
func errorDetail(resp *http.Response) string {
	limited := io.LimitReader(resp.Body, maxErrorBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "<тело недоступно>"
	}
	return strings.TrimSpace(strings.Map(stripControl, string(body)))
}

// stripControl убирает управляющие символы из фрагмента тела ошибки.
func stripControl(r rune) rune {
	if r == '\n' || r == '\r' || r == '\t' {
		return ' '
	}
	if r < 0x20 {
		return -1
	}
	return r
}
