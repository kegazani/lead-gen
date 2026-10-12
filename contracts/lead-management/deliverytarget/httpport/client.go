// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// delivery-target.
//
// Адаптер реализует deliverytarget.Resolver поверх net/http: ResolveRecipient
// идёт в POST {base}/delivery-target. Он нужен, когда потребитель (мост
// delivery-bridge контекста message-delivery) и владелец адреса
// (lead-management) запущены раздельно и прямой вызов недоступен, а контракт
// между ними остаётся тем же (design.md, Решение 2).
//
// HTTP-коды владельца адаптер переводит в исходы контракта: доменный отказ (400 —
// неразбираемая ссылка, 404 — неизвестная ссылка) — ErrRejected, любой другой код
// и сбой сети — ErrUnavailable. Успешный, но пустой адрес — тоже ErrUnavailable:
// адрес не выдумывается. Пакет зависит только от стандартной библиотеки и самого
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

	"lead-gen/contracts/lead-management/deliverytarget"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер deliverytarget.Resolver к сервису lead-management.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ deliverytarget.Resolver = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису разрешения адреса по базовому адресу.
// Базовый адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке
// сервиса, а не при первом разрешении.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса разрешения адреса пуст")
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

// ResolveRecipient реализует deliverytarget.Resolver: разрешает ссылку в адрес
// назначения командой POST {base}/delivery-target.
func (c *Client) ResolveRecipient(ctx context.Context, tenantID, recipientRef string) (deliverytarget.Recipient, error) {
	payload, err := json.Marshal(resolveRequest{TenantID: tenantID, RecipientRef: recipientRef})
	if err != nil {
		return deliverytarget.Recipient{}, fmt.Errorf("%w: сборка тела команды: %v", deliverytarget.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/delivery-target", bytes.NewReader(payload))
	if err != nil {
		return deliverytarget.Recipient{}, fmt.Errorf("%w: сборка запроса: %v", deliverytarget.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return deliverytarget.Recipient{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return deliverytarget.Recipient{}, errorForStatus(resp)
	}

	var body resolveResponse
	if err := decodeBody(resp, &body); err != nil {
		return deliverytarget.Recipient{}, err
	}
	address := strings.TrimSpace(body.Address)
	if address == "" {
		// Владелец ответил успехом, но адреса не дал: это недоступность, а не
		// разрешённый получатель — пустой адрес не подставляется.
		return deliverytarget.Recipient{}, fmt.Errorf("%w: владелец не вернул адрес", deliverytarget.ErrUnavailable)
	}
	return deliverytarget.Recipient{Address: address}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне ResolveRecipient.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", deliverytarget.ErrUnavailable, err)
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
// Доменный отказ различим: 400 — неразбираемая ссылка, 404 — неизвестная ссылка.
// Любой другой код, включая 5xx, означает недоступность владельца, а не
// выдуманный адрес.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", deliverytarget.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", deliverytarget.ErrUnavailable, detail)
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
		return fmt.Errorf("%w: пустой ответ владельца", deliverytarget.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", deliverytarget.ErrUnavailable, err)
	}
	return nil
}

// resolveRequest — тело команды. Имена полей совпадают с контрактом эндпоинта
// lead-management POST /delivery-target.
type resolveRequest struct {
	TenantID     string `json:"tenant_id"`
	RecipientRef string `json:"recipient_ref"`
}

// resolveResponse — ответ владельца на команду разрешения (200 OK).
type resolveResponse struct {
	Address string `json:"address"`
}
