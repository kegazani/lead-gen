// Package httpport — сетевой HTTP-адаптер опубликованного контракта content-request.
//
// Адаптер реализует contentrequest.Port поверх net/http: RequestContent идёт в
// POST {base}/content/produce к владельцу — сервису content-production. Он нужен,
// когда потребитель (request-fulfillment) и владелец контента запущены раздельно и
// прямой вызов недоступен, а контракт между ними остаётся тем же.
//
// HTTP-коды владельца адаптер переводит в исходы контракта: доменный отказ (400)
// — ErrRejected, любой другой неуспешный код и сбой сети — ErrUnavailable. Пакет
// зависит только от стандартной библиотеки и самого контракта и не несёт доменных
// типов владельца.
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

	"lead-gen/contracts/content-production/contentrequest"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер contentrequest.Port к сервису content-production.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ contentrequest.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису производства контента по базовому
// адресу. Базовый адрес обязан быть абсолютным http(s)-URL: ошибка видна при
// сборке сервиса, а не при первой команде.
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

// RequestContent реализует contentrequest.Port: производит контент командой
// POST {base}/content/produce. Идентификатор запроса уходит и телом, и ключом
// Idempotency-Key, поэтому повтор команды безопасен.
func (c *Client) RequestContent(ctx context.Context, in contentrequest.Intent) (contentrequest.Result, error) {
	payload, err := json.Marshal(produceRequest{
		RequestID:     in.RequestID,
		AssetID:       in.AssetID,
		TenantID:      in.TenantID,
		Language:      in.Language,
		Region:        in.Region,
		Format:        in.Format,
		TemplateID:    in.TemplateID,
		Body:          in.Body,
		Mode:          in.Mode,
		Subject:       in.Subject,
		Actor:         in.Actor,
		CorrelationID: in.CorrelationID,
	})
	if err != nil {
		return contentrequest.Result{}, fmt.Errorf("%w: сборка тела команды: %v", contentrequest.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/content/produce", bytes.NewReader(payload))
	if err != nil {
		return contentrequest.Result{}, fmt.Errorf("%w: сборка запроса: %v", contentrequest.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if in.RequestID != "" {
		req.Header.Set("Idempotency-Key", in.RequestID)
	}

	resp, err := c.do(req)
	if err != nil {
		return contentrequest.Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return contentrequest.Result{}, errorForStatus(resp)
	}

	var body produceResponse
	if err := decodeBody(resp, &body); err != nil {
		return contentrequest.Result{}, err
	}
	return contentrequest.Result{
		RequestID:   in.RequestID,
		AssetID:     body.AssetID,
		VersionID:   body.VersionID,
		RenderJobID: body.RenderJobID,
		State:       body.State,
	}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне RequestContent.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", contentrequest.ErrUnavailable, err)
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
// владельца, а не выдуманный результат.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: %s", contentrequest.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", contentrequest.ErrUnavailable, detail)
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

// decodeBody читает полезную нагрузку успешного ответа. Неразборчивый ответ
// владельца — сбой транспорта, а не доменный исход: ErrUnavailable.
func decodeBody(resp *http.Response, dst any) error {
	if resp.Body == nil {
		return fmt.Errorf("%w: пустой ответ владельца", contentrequest.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", contentrequest.ErrUnavailable, err)
	}
	return nil
}

// produceRequest — тело команды. Имена полей совпадают с контрактом эндпоинта
// content-production POST /content/produce.
type produceRequest struct {
	RequestID     string  `json:"request_id"`
	AssetID       string  `json:"asset_id"`
	TenantID      string  `json:"tenant_id"`
	Language      string  `json:"language"`
	Region        string  `json:"region,omitempty"`
	Format        string  `json:"format"`
	TemplateID    string  `json:"template_id"`
	Body          string  `json:"body,omitempty"`
	Mode          string  `json:"mode,omitempty"`
	Subject       *string `json:"subject,omitempty"`
	Actor         string  `json:"actor"`
	CorrelationID string  `json:"correlation_id,omitempty"`
}

// produceResponse — ответ владельца на команду производства (202 Accepted).
type produceResponse struct {
	AssetID     string `json:"asset_id"`
	VersionID   string `json:"version_id"`
	RenderJobID string `json:"render_job_id"`
	State       string `json:"state"`
}
