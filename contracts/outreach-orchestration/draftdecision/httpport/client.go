// Package httpport — сетевой HTTP-адаптер read-контракта draftdecision.
//
// Адаптер реализует draftdecision.Source поверх net/http: ApprovedAction идёт в
// GET {base}/approved-action?tenant_id=..&draft_ref=..&draft_version=.. к владельцу —
// сервису outreach-orchestration. Он нужен, когда потребитель (agent-orchestration)
// и владелец решения запущены раздельно и прямой вызов недоступен, а контракт между
// ними остаётся тем же. In-process адаптер (draftdecision/inproc) сохраняется для
// co-location.
//
// Исходы владельца адаптер переводит в исходы контракта: положительное решение и
// исходы pending/rejected приходят телом Decision (HTTP 200); 404 — ErrNotFound;
// прочие коды и сбой сети — ErrUnavailable. Недоступность не выдаётся за «решение
// не принято». Пакет зависит только от стандартной библиотеки и самого контракта.
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

	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
)

const (
	// approvedActionPath — путь чтения решения у владельца.
	approvedActionPath = "/approved-action"

	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер draftdecision.Source к сервису outreach-orchestration.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ draftdecision.Source = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису владельца по базовому адресу. Базовый
// адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке сервиса, а не
// при первом чтении.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса владельца решения пуст")
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

// ApprovedAction реализует draftdecision.Source: читает решение по черновику
// запросом GET {base}/approved-action.
//
// Порядок шагов: проверка формы запроса (без сети), отправка, перевод кода ответа
// и исхода решения в результат/ошибку контракта.
func (c *Client) ApprovedAction(ctx context.Context, in draftdecision.Lookup) (approvedaction.ApprovedAction, error) {
	if strings.TrimSpace(in.TenantID) == "" || strings.TrimSpace(in.DraftRef) == "" || in.DraftVersion <= 0 {
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: тенант, ссылка черновика и версия обязательны", draftdecision.ErrNotFound)
	}

	values := url.Values{}
	values.Set("tenant_id", in.TenantID)
	values.Set("draft_ref", in.DraftRef)
	values.Set("draft_version", strconv.Itoa(in.DraftVersion))
	target := c.baseURL + approvedActionPath + "?" + values.Encode()

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodGet, target, nil)
	if err != nil {
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: сборка запроса: %v", draftdecision.ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return approvedaction.ApprovedAction{}, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var decision draftdecision.Decision
		if err := decodeBody(resp, &decision); err != nil {
			return approvedaction.ApprovedAction{}, err
		}
		return fromDecision(decision)
	case resp.StatusCode == http.StatusNotFound:
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %s", draftdecision.ErrNotFound, errorDetail(resp))
	default:
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %s", draftdecision.ErrUnavailable, errorDetail(resp))
	}
}

// fromDecision переводит исход чтения в результат/ошибку контракта. Неизвестный
// исход — сбой транспорта, а не выдуманное решение.
func fromDecision(decision draftdecision.Decision) (approvedaction.ApprovedAction, error) {
	switch decision.Outcome {
	case draftdecision.OutcomeApproved:
		if decision.ApprovedAction == nil {
			return approvedaction.ApprovedAction{}, fmt.Errorf("%w: положительное решение без утверждённого действия", draftdecision.ErrUnavailable)
		}
		return *decision.ApprovedAction, nil
	case draftdecision.OutcomePending:
		return approvedaction.ApprovedAction{}, draftdecision.ErrPending
	case draftdecision.OutcomeRejected:
		return approvedaction.ApprovedAction{}, draftdecision.ErrRejected
	default:
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: неизвестный исход %q", draftdecision.ErrUnavailable, decision.Outcome)
	}
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода — на
// стороне ApprovedAction.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", draftdecision.ErrUnavailable, err)
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
// владельца — сбой транспорта, а не доменный исход: ErrUnavailable.
func decodeBody(resp *http.Response, dst any) error {
	if resp.Body == nil {
		return fmt.Errorf("%w: пустой ответ владельца", draftdecision.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", draftdecision.ErrUnavailable, err)
	}
	return nil
}
