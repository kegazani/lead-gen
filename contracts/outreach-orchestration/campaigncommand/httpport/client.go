// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// campaign-command.
//
// Адаптер реализует campaigncommand.Port поверх net/http: SubmitForApproval идёт
// в POST {base}/campaign-command к владельцу — сервису outreach-orchestration. Он
// нужен, когда инициатор (agent-orchestration) и владелец очереди утверждения
// запущены раздельно и прямой вызов недоступен, а контракт между ними остаётся тем
// же. In-process адаптер (campaigncommand/inproc) сохраняется для co-location.
//
// HTTP-коды владельца адаптер переводит в исходы контракта: доменный отказ (400) —
// ErrRejected, любой другой код и сбой сети — ErrUnavailable. Невалидная команда не
// отправляется вовсе: ошибка видна вызывающему локально. Пакет зависит только от
// стандартной библиотеки и самого контракта.
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

	"lead-gen/contracts/outreach-orchestration/campaigncommand"
)

const (
	// submitPath — путь приёма команды у владельца.
	submitPath = "/campaign-command"

	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер campaigncommand.Port к сервису outreach-orchestration.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ campaigncommand.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису владельца по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первой подаче.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса владельца campaign-command пуст")
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

// SubmitForApproval реализует campaigncommand.Port: подаёт черновик командой
// POST {base}/campaign-command. Невалидная команда не отправляется. Ключ
// идемпотентности уходит и в теле (компоненты), и заголовком Idempotency-Key,
// поэтому повтор команды безопасен.
func (c *Client) SubmitForApproval(ctx context.Context, in campaigncommand.Submission) (campaigncommand.Result, error) {
	if err := in.Validate(); err != nil {
		return campaigncommand.Result{}, err
	}

	payload, err := json.Marshal(submissionWireFrom(in))
	if err != nil {
		return campaigncommand.Result{}, fmt.Errorf("%w: сборка тела команды: %v", campaigncommand.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+submitPath, bytes.NewReader(payload))
	if err != nil {
		return campaigncommand.Result{}, fmt.Errorf("%w: сборка запроса: %v", campaigncommand.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key := in.IdempotencyKey(); key != "" {
		req.Header.Set("Idempotency-Key", key)
	}

	resp, err := c.do(req)
	if err != nil {
		return campaigncommand.Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return campaigncommand.Result{}, errorForStatus(resp)
	}

	var body resultWire
	if err := decodeBody(resp, &body); err != nil {
		return campaigncommand.Result{}, err
	}
	return campaigncommand.Result{
		CampaignID:   body.CampaignID,
		DraftRef:     body.DraftRef,
		DraftVersion: body.DraftVersion,
		Repeated:     body.Repeated,
	}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода — на
// стороне SubmitForApproval.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", campaigncommand.ErrUnavailable, err)
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
// владельца, а не выдуманный приём.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: %s", campaigncommand.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", campaigncommand.ErrUnavailable, detail)
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
		return fmt.Errorf("%w: пустой ответ владельца", campaigncommand.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", campaigncommand.ErrUnavailable, err)
	}
	return nil
}

// submissionWireFrom переводит команду в проводную форму. Форма совпадает с телом,
// которое принимает владелец на POST /campaign-command.
func submissionWireFrom(in campaigncommand.Submission) submissionWire {
	return submissionWire{
		TenantID:     in.TenantID,
		RunID:        in.RunID,
		MissionID:    in.MissionID,
		CompanyID:    in.CompanyID,
		DraftRef:     in.DraftRef,
		DraftVersion: in.DraftVersion,
		MaterialRef:  in.MaterialRef,
		RecipientRef: in.RecipientRef,
	}
}

// submissionWire — тело команды. Имена полей совпадают с контрактом эндпоинта
// outreach-orchestration POST /campaign-command.
type submissionWire struct {
	TenantID     string `json:"tenant_id"`
	RunID        string `json:"run_id"`
	MissionID    string `json:"mission_id,omitempty"`
	CompanyID    string `json:"company_id"`
	DraftRef     string `json:"draft_ref"`
	DraftVersion int    `json:"draft_version"`
	MaterialRef  string `json:"material_ref"`
	RecipientRef string `json:"recipient_ref,omitempty"`
}

// resultWire — ответ владельца на команду (200 OK).
type resultWire struct {
	CampaignID   string `json:"campaign_id"`
	DraftRef     string `json:"draft_ref"`
	DraftVersion int    `json:"draft_version"`
	Repeated     bool   `json:"repeated"`
}
