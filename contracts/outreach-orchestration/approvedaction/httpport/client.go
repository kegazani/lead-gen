// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// approved-action.
//
// Адаптер реализует approvedaction.Publisher поверх net/http: Publish идёт в
// POST {base}/approved-action. Он нужен, когда владелец контракта
// (outreach-orchestration) и исполнитель (message-delivery) запущены раздельно и
// прямой вызов недоступен, а контракт между ними остаётся тем же
// (design.md, Решение 2). In-process адаптер (approvedaction/inproc) сохраняется
// для co-location.
//
// HTTP-коды приёмной стороны адаптер переводит в исходы контракта: доменный отказ
// (400) — ErrRejected, любой другой код и сбой сети — ErrUnavailable. Невалидный
// контракт не отправляется вовсе: ошибка видна вызывающему локально. Пакет
// зависит только от стандартной библиотеки и самого контракта и не несёт доменных
// типов владельца.
package httpport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

const (
	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10

	// publishPath — путь приёма утверждённых действий у message-delivery.
	publishPath = "/approved-action"
)

// Исходы сетевого адаптера. Доменный отказ (локальная проверка контракта или
// HTTP 400 приёмной стороны) отличим от недоступности (сбой сети или иной код):
// недоступность не выдаётся за отказ, а отказ — за недоступность.
var (
	// ErrRejected — действие отклонено: локально (не прошло Validate) или
	// приёмной стороной (HTTP 400).
	ErrRejected = errors.New("httpport: approved-action отклонён")

	// ErrUnavailable — приёмная сторона недоступна: сбой сети или код, отличный
	// от 200/204/400. Результат не выдумывается.
	ErrUnavailable = errors.New("httpport: сервис приёма approved-action недоступен")
)

// Client — сетевой адаптер approvedaction.Publisher к сервису message-delivery.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ approvedaction.Publisher = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису приёма по базовому адресу. Базовый адрес
// обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не при
// первой публикации.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса приёма approved-action пуст")
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

// Publish реализует approvedaction.Publisher: доставляет утверждённое действие
// командой POST {base}/approved-action.
//
// Порядок шагов: проверка контракта (без сети), сериализация, отправка и перевод
// кода ответа в исход. Невалидный контракт не отправляется: ошибка возвращается
// локально и не «съедает» сетевой вызов. Успех (200/204) — nil; 400 — доменный
// отказ; прочие коды и сбой сети — недоступность.
func (c *Client) Publish(ctx context.Context, action approvedaction.ApprovedAction) error {
	if err := action.Validate(); err != nil {
		return fmt.Errorf("%w: невалидный контракт: %v", ErrRejected, err)
	}

	payload, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("%w: сборка тела команды: %v", ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+publishPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: сборка запроса: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent:
		return nil
	case resp.StatusCode == http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrRejected, errorDetail(resp))
	default:
		return fmt.Errorf("%w: %s", ErrUnavailable, errorDetail(resp))
	}
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне Publish.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
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

// errorDetail достаёт сообщение приёмной стороны из тела ошибки, не доверяя его
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
