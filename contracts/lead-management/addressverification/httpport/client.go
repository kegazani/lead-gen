// Package httpport — сетевой HTTP-адаптер опубликованного контракта
// address-verification.
//
// Адаптер реализует addressverification.Port поверх net/http: Verification идёт
// в POST {base}/address-verification. Он нужен, когда потребитель (сборщик фактов
// риска outreach-orchestration) и владелец верификации (lead-management)
// запущены раздельно и прямой вызов недоступен, а контракт между ними остаётся
// тем же (design.md, Решение 1).
//
// HTTP-коды владельца адаптер переводит в исходы контракта: доменный отказ (400 —
// неразбираемый запрос или неизвестная ссылка, 404) — ErrRejected, любой другой
// код и сбой сети — ErrUnavailable. Успешный, но нераспознанный ответ (нет
// состояния верификации или неизвестный статус, неразборчивое тело) — тоже
// ErrUnavailable: вердикт владельца не выдумывается и не трактуется как
// «подтверждён». Пакет зависит только от стандартной библиотеки и самого
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
	"time"

	"lead-gen/contracts/lead-management/addressverification"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца, чтобы
	// сбойный ответ не мог исчерпать память потребителя.
	maxBodyBytes = 1 << 20

	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер addressverification.Port к сервису lead-management.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ addressverification.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису верификации адреса по базовому адресу.
// Базовый адрес обязан быть абсолютным http(s)-URL: ошибка видна при сборке
// сервиса, а не при первом запросе.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса верификации адреса пуст")
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

// Verification реализует addressverification.Port: запрашивает состояние
// верификации получателя запросом POST {base}/address-verification.
func (c *Client) Verification(ctx context.Context, q addressverification.Query) (addressverification.Snapshot, error) {
	payload, err := json.Marshal(verificationRequest{TenantID: q.TenantID, RecipientRef: q.RecipientRef})
	if err != nil {
		return addressverification.Snapshot{}, fmt.Errorf("%w: сборка тела запроса: %v", addressverification.ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodPost, c.baseURL+"/address-verification", bytes.NewReader(payload))
	if err != nil {
		return addressverification.Snapshot{}, fmt.Errorf("%w: сборка запроса: %v", addressverification.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return addressverification.Snapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return addressverification.Snapshot{}, errorForStatus(resp)
	}

	var body verificationResponse
	if err := decodeBody(resp, &body); err != nil {
		return addressverification.Snapshot{}, err
	}

	status := addressverification.Status(strings.TrimSpace(body.Status))
	if !status.Known() {
		// Владелец ответил успехом, но состояния верификации не дал: это
		// недоступность, а не вердикт — статус не выдумывается и адрес не
		// трактуется ни как подтверждённый, ни как неподтверждённый.
		return addressverification.Snapshot{}, fmt.Errorf("%w: владелец не вернул распознанный статус верификации", addressverification.ErrUnavailable)
	}

	checkedAt, err := parseCheckedAt(body.CheckedAt)
	if err != nil {
		return addressverification.Snapshot{}, err
	}

	ref := strings.TrimSpace(body.RecipientRef)
	if ref == "" {
		ref = q.RecipientRef
	}
	return addressverification.Snapshot{
		RecipientRef: ref,
		Status:       status,
		Verified:     body.Verified,
		Reason:       body.Reason,
		CheckedAt:    checkedAt,
	}, nil
}

// parseCheckedAt разбирает необязательное время проверки. Пустое значение — нулевое
// время («не проверялся»); неразборчивое — сбой транспорта, а не выдуманный момент.
func parseCheckedAt(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: разбор времени проверки: %v", addressverification.ErrUnavailable, err)
	}
	return parsed, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable. Ответ с любым HTTP-кодом отдаётся вызывающему: разбор кода —
// на стороне Verification.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", addressverification.ErrUnavailable, err)
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
// Доменный отказ различим: 400 и 404 — отклонённый/неизвестный запрос. Любой
// другой код, включая 5xx, означает недоступность владельца, а не выдуманный
// вердикт.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", addressverification.ErrRejected, detail)
	}
	return fmt.Errorf("%w: %s", addressverification.ErrUnavailable, detail)
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
		return fmt.Errorf("%w: пустой ответ владельца", addressverification.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", addressverification.ErrUnavailable, err)
	}
	return nil
}

// verificationRequest — тело запроса. Имена полей совпадают с контрактом эндпоинта
// lead-management POST /address-verification.
type verificationRequest struct {
	TenantID     string `json:"tenant_id"`
	RecipientRef string `json:"recipient_ref"`
}

// verificationResponse — ответ владельца (200 OK). Адреса нет: только ссылка,
// статус и вердикт владельца.
type verificationResponse struct {
	RecipientRef string `json:"recipient_ref"`
	Status       string `json:"status"`
	Verified     bool   `json:"verified"`
	Reason       string `json:"reason"`
	CheckedAt    string `json:"checked_at"`
}
