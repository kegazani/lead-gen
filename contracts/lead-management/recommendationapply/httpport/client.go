// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// recommendation-apply.
//
// Адаптер реализует recommendationapply.Port поверх net/http:
// ApplyRecommendation идёт в POST {base}/recommendation-apply. Он нужен, когда
// потребитель (analytics) и владелец контура поиска (lead-management) запущены
// раздельно и прямой вызов недоступен, а контракт между ними остаётся тем же.
//
// HTTP-коды владельца адаптер переводит в исходы контракта: доменный отказ (400)
// — ErrRejected, любой другой код и сбой сети — ErrUnavailable. Пакет зависит
// только от стандартной библиотеки и самого контракта.
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

	"lead-gen/contracts/lead-management/recommendationapply"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца.
	maxBodyBytes = 1 << 20
	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер recommendationapply.Port к сервису lead-management.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ recommendationapply.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису поиска по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первом применении.
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

// ApplyRecommendation реализует recommendationapply.Port: применяет рекомендацию
// командой POST {base}/recommendation-apply.
func (c *Client) ApplyRecommendation(ctx context.Context, application recommendationapply.Application) (recommendationapply.Receipt, error) {
	payload, err := json.Marshal(applicationRequest{
		TenantID:  application.TenantID,
		ChangeID:  application.ChangeID,
		ScopeKind: application.ScopeKind,
		ScopeKey:  application.ScopeKey,
		Reason:    application.Reason,
		Basis:     application.Basis,
		Change: changeWire{
			Kind:     application.Change.Kind,
			Criteria: application.Change.Criteria,
			Targets:  application.Change.Targets,
			Limit:    application.Change.Limit,
		},
	})
	if err != nil {
		return recommendationapply.Receipt{}, fmt.Errorf("%w: сборка тела команды: %v", recommendationapply.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/recommendation-apply", bytes.NewReader(payload))
	if err != nil {
		return recommendationapply.Receipt{}, fmt.Errorf("%w: сборка запроса: %v", recommendationapply.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return recommendationapply.Receipt{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return recommendationapply.Receipt{}, errorForStatus(resp)
	}

	var body receiptResponse
	if err := decodeBody(resp, &body); err != nil {
		return recommendationapply.Receipt{}, err
	}
	return recommendationapply.Receipt{JobID: body.JobID}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", recommendationapply.ErrUnavailable, err)
	}
	return resp, nil
}

// normalizeContext подставляет фон, если вызов пришёл без контекста.
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// errorForStatus переводит код неуспешного ответа владельца в исход контракта.
// Доменный отказ различим; любой другой код, включая 5xx, означает недоступность
// владельца, а не выдуманное применение.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: %s", recommendationapply.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", recommendationapply.ErrUnavailable, detail)
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

// decodeBody читает полезную нагрузку успешного ответа. Неразборчивый ответ
// владельца — сбой транспорта: ErrUnavailable.
func decodeBody(resp *http.Response, dst any) error {
	if resp.Body == nil {
		return fmt.Errorf("%w: пустой ответ владельца", recommendationapply.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", recommendationapply.ErrUnavailable, err)
	}
	return nil
}

// applicationRequest — тело команды. Имена полей совпадают с контрактом
// эндпоинта lead-management POST /recommendation-apply.
type applicationRequest struct {
	TenantID  string     `json:"tenant_id"`
	ChangeID  string     `json:"change_id"`
	ScopeKind string     `json:"scope_kind,omitempty"`
	ScopeKey  string     `json:"scope_key,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Basis     string     `json:"basis,omitempty"`
	Change    changeWire `json:"change"`
}

// changeWire — структурное изменение в теле команды.
type changeWire struct {
	Kind     string   `json:"kind"`
	Criteria string   `json:"criteria,omitempty"`
	Targets  []string `json:"targets,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// receiptResponse — ответ владельца на команду применения (2xx).
type receiptResponse struct {
	JobID string `json:"job_id"`
}
