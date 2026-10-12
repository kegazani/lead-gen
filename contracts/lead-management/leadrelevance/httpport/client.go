// Package httpport — сетевой HTTP-адаптер опубликованного read-контракта
// lead-relevance.
//
// Адаптер реализует leadrelevance.Port поверх net/http: Read идёт в
// GET {base}/lead-relevance?job_id=&company_id=. Он нужен, когда потребитель
// (блок `classify` агента) и владелец оценки (lead-management) запущены
// раздельно, а контракт между ними остаётся тем же.
//
// HTTP-коды владельца адаптер переводит в исходы контракта: 400 — ErrRejected,
// 404 — ErrNotFound, любой другой код и сбой сети — ErrUnavailable. Пакет зависит
// только от стандартной библиотеки и самого контракта.
package httpport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lead-gen/contracts/lead-management/leadrelevance"
)

const (
	// maxBodyBytes ограничивает размер разбираемого ответа владельца.
	maxBodyBytes = 1 << 20
	// maxErrorBytes ограничивает фрагмент тела ошибки, попадающий в текст.
	maxErrorBytes = 4 << 10
)

// Client — сетевой адаптер leadrelevance.Port к сервису lead-management.
// Безопасен для одновременного использования.
type Client struct {
	baseURL string
	http    *http.Client
}

// Проверка соответствия контракту на этапе компиляции.
var _ leadrelevance.Port = (*Client)(nil)

// Option настраивает Client.
type Option func(*Client)

// WithHTTPClient подменяет HTTP-клиент (прокси, инструментация, тесты). nil
// означает http.DefaultClient.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// New собирает сетевой адаптер к сервису оценки по базовому адресу. Базовый адрес
// обязан быть абсолютным http(s)-URL.
func New(baseURL string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpport: базовый адрес сервиса оценки пуст")
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

// Read реализует leadrelevance.Port: читает оценку по паре (задание, компания).
func (c *Client) Read(ctx context.Context, request leadrelevance.Request) (leadrelevance.Assessment, error) {
	endpoint := c.baseURL + "/lead-relevance?" + url.Values{
		"job_id":     {request.JobID},
		"company_id": {request.CompanyID},
	}.Encode()

	req, err := http.NewRequestWithContext(normalizeContext(ctx), http.MethodGet, endpoint, nil)
	if err != nil {
		return leadrelevance.Assessment{}, fmt.Errorf("%w: сборка запроса: %v", leadrelevance.ErrUnavailable, err)
	}

	resp, err := c.do(req)
	if err != nil {
		return leadrelevance.Assessment{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return leadrelevance.Assessment{}, errorForStatus(resp)
	}

	var body assessmentResponse
	if err := decodeBody(resp, &body); err != nil {
		return leadrelevance.Assessment{}, err
	}
	evidence := make([]leadrelevance.Evidence, 0, len(body.Evidence))
	for _, e := range body.Evidence {
		evidence = append(evidence, leadrelevance.Evidence{
			Criterion:  e.Criterion,
			Fact:       e.Fact,
			SourceURL:  e.SourceURL,
			Reasoning:  e.Reasoning,
			SignalKind: e.SignalKind,
			Missing:    e.Missing,
			Mismatch:   e.Mismatch,
		})
	}
	return leadrelevance.Assessment{
		Contract:      body.Contract,
		SchemaVersion: body.SchemaVersion,
		JobID:         body.JobID,
		CompanyID:     body.CompanyID,
		Verdict:       body.Verdict,
		SignalKind:    body.SignalKind,
		Evidence:      evidence,
		AssessedAt:    body.AssessedAt,
	}, nil
}

// do выполняет запрос, соблюдая контекст вызова, и переводит сбой сети в
// ErrUnavailable.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", leadrelevance.ErrUnavailable, err)
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
// 400 — доменный отказ, 404 — оценки нет; любой другой код, включая 5xx, означает
// недоступность владельца.
func errorForStatus(resp *http.Response) error {
	detail := errorDetail(resp)
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", leadrelevance.ErrRejected, detail)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", leadrelevance.ErrNotFound, detail)
	default:
		return fmt.Errorf("%w: %s", leadrelevance.ErrUnavailable, detail)
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

// decodeBody читает полезную нагрузку успешного ответа.
func decodeBody(resp *http.Response, dst any) error {
	if resp.Body == nil {
		return fmt.Errorf("%w: пустой ответ владельца", leadrelevance.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: разбор ответа владельца: %v", leadrelevance.ErrUnavailable, err)
	}
	return nil
}

// evidenceWire — свидетельство в ответе владельца.
type evidenceWire struct {
	Criterion  string `json:"criterion"`
	Fact       string `json:"fact"`
	SourceURL  string `json:"source_url"`
	Reasoning  string `json:"reasoning"`
	SignalKind string `json:"signal_kind"`
	Missing    string `json:"missing,omitempty"`
	Mismatch   string `json:"mismatch,omitempty"`
}

// assessmentResponse — ответ владельца на запрос оценки (200 OK).
type assessmentResponse struct {
	Contract      string         `json:"contract"`
	SchemaVersion string         `json:"schema_version"`
	JobID         string         `json:"job_id"`
	CompanyID     string         `json:"company_id"`
	Verdict       string         `json:"verdict"`
	SignalKind    string         `json:"signal_kind"`
	Evidence      []evidenceWire `json:"evidence"`
	AssessedAt    time.Time      `json:"assessed_at"`
}
