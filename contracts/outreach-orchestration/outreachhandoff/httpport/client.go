// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// outreach-handoff.
//
// Адаптер реализует outreachhandoff.Port поверх net/http: Submit идёт в
// POST {base}/outreach-handoff к владельцу — сервису outreach-orchestration. Он
// нужен, когда владелец передачи (request-fulfillment) и получатель
// (outreach-orchestration) запущены раздельно и прямой вызов недоступен, а
// контракт между ними остаётся тем же.
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

	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер outreachhandoff.Port к сервису outreach-orchestration.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ outreachhandoff.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису передачи по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первой передаче.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса передачи пуст")
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

// Submit реализует outreachhandoff.Port: передаёт письма заявки командой
// POST {base}/outreach-handoff. Идентификатор заявки уходит и телом, и ключом
// Idempotency-Key, поэтому повтор команды безопасен.
func (c *Client) Submit(ctx context.Context, command outreachhandoff.Command) (outreachhandoff.Result, error) {
	payload, err := json.Marshal(submitRequest{
		RequestID:        command.RequestID,
		TenantID:         command.TenantID,
		Actor:            command.Actor,
		Name:             command.Name,
		ExecutionPurpose: command.ExecutionPurpose,
		Items:            itemsToWire(command.Items),
	})
	if err != nil {
		return outreachhandoff.Result{}, fmt.Errorf("%w: сборка тела команды: %v", outreachhandoff.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/outreach-handoff", bytes.NewReader(payload))
	if err != nil {
		return outreachhandoff.Result{}, fmt.Errorf("%w: сборка запроса: %v", outreachhandoff.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if command.RequestID != "" {
		req.Header.Set("Idempotency-Key", command.RequestID)
	}

	resp, err := c.do(req)
	if err != nil {
		return outreachhandoff.Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return outreachhandoff.Result{}, errorForStatus(resp)
	}

	var body submitResponse
	if err := decodeBody(resp, &body); err != nil {
		return outreachhandoff.Result{}, err
	}
	return outreachhandoff.Result{
		CampaignID: body.CampaignID,
		Accepted:   body.Accepted,
		Skipped:    body.Skipped,
		Repeated:   body.Repeated,
	}, nil
}

// CancelRequest durably asks the owner to fence an entire request, including a
// handoff that has not arrived yet. A successful response acknowledges the
// tombstone commit, not retraction of an already-started email.
func (c *Client) CancelRequest(ctx context.Context, cancellation outreachhandoff.RequestCancellation) error {
	if err := cancellation.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(cancelRequest{TenantID: cancellation.TenantID, RequestID: cancellation.RequestID})
	if err != nil {
		return fmt.Errorf("%w: сборка тела отмены: %v", outreachhandoff.ErrUnavailable, err)
	}
	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/outreach-handoff/cancellation", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: сборка запроса отмены: %v", outreachhandoff.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", cancellation.TenantID+"/"+cancellation.RequestID)
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errorForStatus(resp)
	}
	return nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне Submit.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", outreachhandoff.ErrUnavailable, err)
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
// владельца, а не выдуманную передачу.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: %s", outreachhandoff.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", outreachhandoff.ErrUnavailable, detail)
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
		return fmt.Errorf("%w: пустой ответ владельца", outreachhandoff.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", outreachhandoff.ErrUnavailable, err)
	}
	return nil
}

// itemsToWire переводит состав передачи в проводную форму.
func itemsToWire(items []outreachhandoff.Item) []itemWire {
	wire := make([]itemWire, 0, len(items))
	for _, item := range items {
		wire = append(wire, itemWire{
			LeadID:       item.LeadID,
			RecipientRef: item.RecipientRef,
			MaterialRef:  item.MaterialRef,
		})
	}
	return wire
}

// submitRequest — тело команды. Имена полей совпадают с контрактом эндпоинта
// outreach-orchestration POST /outreach-handoff.
type submitRequest struct {
	RequestID        string     `json:"request_id"`
	TenantID         string     `json:"tenant_id"`
	Actor            string     `json:"actor,omitempty"`
	Name             string     `json:"name,omitempty"`
	ExecutionPurpose string     `json:"execution_purpose,omitempty"`
	Items            []itemWire `json:"items"`
}

type cancelRequest struct {
	TenantID  string `json:"tenant_id"`
	RequestID string `json:"request_id"`
}

// itemWire — проводная форма одного письма передачи.
type itemWire struct {
	LeadID       string `json:"lead_id"`
	RecipientRef string `json:"recipient_ref"`
	MaterialRef  string `json:"material_ref"`
}

// submitResponse — ответ владельца на команду передачи (200 OK).
type submitResponse struct {
	CampaignID string `json:"campaign_id"`
	Accepted   int    `json:"accepted"`
	Skipped    int    `json:"skipped"`
	Repeated   bool   `json:"repeated"`
}
