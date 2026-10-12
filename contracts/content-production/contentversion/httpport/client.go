// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// content-version.
//
// Адаптер реализует contentversion.Source поверх net/http: Version идёт в
// POST {base}/content-version. Он нужен, когда потребитель (message-delivery) и
// владелец контента (content-production) запущены раздельно и прямой вызов
// недоступен, а контракт между ними остаётся тем же.
//
// HTTP-коды владельца адаптер переводит в исходы контракта: 404 — ErrNotFound,
// любой другой код и сбой сети — ErrUnavailable. Пакет зависит только от
// стандартной библиотеки и самого контракта и не несёт доменных типов владельца.
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

	"lead-gen/contracts/content-production/contentversion"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер contentversion.Source к сервису content-production.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ contentversion.Source = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису контента по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первом чтении версии.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса контента пуст")
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

// Version реализует contentversion.Source: читает версию контента командой
// POST {base}/content-version. Пустой VersionID означает последнюю версию пакета.
func (c *Client) Version(ctx context.Context, q contentversion.Query) (contentversion.Version, error) {
	payload, err := json.Marshal(versionRequest{
		AssetID:   q.AssetID,
		VersionID: q.VersionID,
		TenantID:  q.TenantID,
	})
	if err != nil {
		return contentversion.Version{}, fmt.Errorf("%w: сборка тела команды: %v", contentversion.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/content-version", bytes.NewReader(payload))
	if err != nil {
		return contentversion.Version{}, fmt.Errorf("%w: сборка запроса: %v", contentversion.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return contentversion.Version{}, err
	}
	defer resp.Body.Close()

	// 404 — доменный исход: версии нет либо пакет принадлежит другому тенанту.
	// «Нет» и «не твоя» неотличимы, существование чужого пакета не раскрывается.
	if resp.StatusCode == http.StatusNotFound {
		return contentversion.Version{}, fmt.Errorf("%w: %s", contentversion.ErrNotFound, errorDetail(resp))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return contentversion.Version{}, fmt.Errorf("%w: %s", contentversion.ErrUnavailable, errorDetail(resp))
	}

	var body versionResponse
	if err := decodeBody(resp, &body); err != nil {
		return contentversion.Version{}, err
	}
	return contentversion.Version{
		AssetID:   body.AssetID,
		VersionID: body.VersionID,
		Revision:  body.Revision,
		Language:  body.Language,
		Region:    body.Region,
		Subject:   body.Subject,
		Body:      body.Body,
	}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне Version.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", contentversion.ErrUnavailable, err)
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
		return fmt.Errorf("%w: пустой ответ владельца", contentversion.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", contentversion.ErrUnavailable, err)
	}
	return nil
}

// versionRequest — тело команды чтения версии. Имена полей совпадают с
// контрактом эндпоинта content-production POST /content-version.
type versionRequest struct {
	AssetID   string `json:"asset_id"`
	VersionID string `json:"version_id"`
	TenantID  string `json:"tenant_id"`
}

// versionResponse — ответ владельца на команду чтения версии (200 OK).
type versionResponse struct {
	AssetID   string `json:"asset_id"`
	VersionID string `json:"version_id"`
	Revision  int    `json:"revision"`
	Language  string `json:"language"`
	Region    string `json:"region"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
}
