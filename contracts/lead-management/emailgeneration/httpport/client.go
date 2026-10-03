// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// email-generation.
//
// Адаптер реализует emailgeneration.Port поверх net/http: Generate идёт в
// POST {base}/email-generation. Он нужен, когда потребитель
// (request-fulfillment) и владелец генерации (lead-management) запущены
// раздельно и прямой вызов недоступен, а контракт между ними остаётся тем же
// (design.md, Решение 2).
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

	"lead-gen/contracts/lead-management/emailgeneration"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер emailgeneration.Port к сервису lead-management.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ emailgeneration.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису генерации по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первом вызове.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса генерации пуст")
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

// Generate реализует emailgeneration.Port: запрашивает письмо командой
// POST {base}/email-generation.
func (c *Client) Generate(ctx context.Context, request emailgeneration.Request) (emailgeneration.Email, error) {
	payload, err := json.Marshal(generateRequest{
		RequestID: request.RequestID,
		TenantID:  request.TenantID,
		LeadID:    request.LeadID,
		Criteria:  request.Criteria,
		Language:  request.Language,
		Tone:      request.Tone,
	})
	if err != nil {
		return emailgeneration.Email{}, fmt.Errorf("%w: сборка тела команды: %v", emailgeneration.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/email-generation", bytes.NewReader(payload))
	if err != nil {
		return emailgeneration.Email{}, fmt.Errorf("%w: сборка запроса: %v", emailgeneration.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return emailgeneration.Email{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return emailgeneration.Email{}, errorForStatus(resp)
	}

	var body generateResponse
	if err := decodeBody(resp, &body); err != nil {
		return emailgeneration.Email{}, err
	}
	return emailgeneration.Email{
		Subject:  body.Subject,
		Body:     body.Body,
		Language: body.Language,
	}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне Generate.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", emailgeneration.ErrUnavailable, err)
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
// владельца, а не выдуманное пустое письмо.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: %s", emailgeneration.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", emailgeneration.ErrUnavailable, detail)
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
		return fmt.Errorf("%w: пустой ответ владельца", emailgeneration.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", emailgeneration.ErrUnavailable, err)
	}
	return nil
}

// generateRequest — тело команды. Имена полей совпадают с контрактом эндпоинта
// lead-management POST /email-generation.
type generateRequest struct {
	RequestID string `json:"request_id"`
	TenantID  string `json:"tenant_id"`
	LeadID    string `json:"lead_id"`
	Criteria  string `json:"criteria"`
	Language  string `json:"language,omitempty"`
	Tone      string `json:"tone,omitempty"`
}

// generateResponse — ответ владельца на команду генерации (200 OK).
type generateResponse struct {
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Language string `json:"language,omitempty"`
}
