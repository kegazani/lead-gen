// Package httpport — сетевой HTTP-адаптер опубликованного контракта agent-run-state.
//
// Адаптер реализует agentrunstate.ReadModel поверх net/http: RunState читает
// GET {base}/agent-runs/{runID}, List — GET {base}/agent-runs. Он нужен, когда
// потребитель (консоль операций) и владелец состояния (agent-orchestration)
// запущены раздельно и прямой вызов недоступен, а контракт между ними остаётся тем
// же (design.md, Решение 6).
//
// HTTP-коды владельца адаптер переводит в исходы контракта: 404 — ErrNotFound,
// 403 — ErrForbidden, 400 — ErrRejected, любой другой код и сбой сети —
// ErrUnavailable. Пакет зависит только от стандартной библиотеки и самого
// контракта и не несёт доменных типов владельца.
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

	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы сбойный
	// ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер agentrunstate.ReadModel к сервису agent-orchestration.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ agentrunstate.ReadModel = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису состояния агентов по базовому адресу.
// Базовый адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке
// сервиса, а не при первом чтении.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("agentrunstate/httpport: базовый адрес сервиса пуст")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("agentrunstate/httpport: разбор базового адреса %q: %w", base, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("agentrunstate/httpport: базовый адрес %q должен быть http(s)-URL", base)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("agentrunstate/httpport: базовый адрес %q без хоста", base)
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

// RunState реализует agentrunstate.Reader: читает состояние запуска командой
// GET {base}/agent-runs/{runID}.
func (c *Client) RunState(ctx context.Context, request agentrunstate.Request) (agentrunstate.RunState, error) {
	if err := request.Validate(); err != nil {
		return agentrunstate.RunState{}, fmt.Errorf("%w: %v", agentrunstate.ErrRejected, err)
	}
	query := url.Values{}
	query.Set("tenant_id", request.TenantID)
	if actor := strings.TrimSpace(request.Actor); actor != "" {
		query.Set("actor", actor)
	}
	target := c.baseURL + "/agent-runs/" + url.PathEscape(request.RunID) + "?" + query.Encode()

	var state agentrunstate.RunState
	if err := c.get(ctx, target, &state); err != nil {
		return agentrunstate.RunState{}, err
	}
	return state, nil
}

// List реализует agentrunstate.Lister: читает ленту запусков командой
// GET {base}/agent-runs.
func (c *Client) List(ctx context.Context, request agentrunstate.ListRequest) ([]agentrunstate.RunSummary, error) {
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", agentrunstate.ErrRejected, err)
	}
	query := url.Values{}
	query.Set("tenant_id", request.TenantID)
	if status := strings.TrimSpace(request.Status); status != "" {
		query.Set("status", status)
	}
	if request.Limit > 0 {
		query.Set("limit", strconv.Itoa(request.Limit))
	}
	if actor := strings.TrimSpace(request.Actor); actor != "" {
		query.Set("actor", actor)
	}
	target := c.baseURL + "/agent-runs?" + query.Encode()

	var result agentrunstate.ListResult
	if err := c.get(ctx, target, &result); err != nil {
		return nil, err
	}
	if result.Runs == nil {
		return []agentrunstate.RunSummary{}, nil
	}
	return result.Runs, nil
}

// get выполняет GET-запрос, соблюдая контекст, и разбирает тело успешного ответа.
func (c *Client) get(ctx context.Context, target string, dst any) error {
	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%w: сборка запроса: %v", agentrunstate.ErrUnavailable, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", agentrunstate.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errorForStatus(resp)
	}
	if resp.Body == nil {
		return fmt.Errorf("%w: пустой ответ владельца", agentrunstate.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", agentrunstate.ErrUnavailable, err)
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

// errorForStatus переводит код неуспешного ответа владельца в исход контракта.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", agentrunstate.ErrNotFound, detail)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", agentrunstate.ErrForbidden, detail)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", agentrunstate.ErrRejected, detail)
	default:
		return fmt.Errorf("%w: %s", agentrunstate.ErrUnavailable, detail)
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
