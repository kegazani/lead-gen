// Package remote реализует ports.AddressVerifier поверх внешнего сервиса
// верификации адресов (Решение 1: собственный SMTP-пробинг не выполняется).
//
// # Честность и конфигурация
//
// Конкретный провайдер — открытый вопрос BRD §8 (вопрос 9a). Адаптер выполняет
// реальный HTTP-запрос к сконфигурированному сервису; при пустом адресе сервиса
// он честно сообщает о недоступности (ports.ErrVerifierUnavailable), а не
// подставляет выдуманный исход. Нераспознанный ответ сервиса также даёт
// недоступность: угадывать статус по незнакомому значению запрещено.
//
// # Договор с сервисом
//
// Запрос: POST <endpoint> с телом {"address":"local@domain"}; заголовок
// Authorization: Bearer <api key>, если ключ задан.
//
// Ответ (JSON): {"outcome":"accepted|rejected|unknown","catch_all":bool,
// "reason":"...","source":"..."}. Исход — нормализованный ответ сервиса о
// получателе и домене; «accepted» не означает, что ящик существует.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// defaultTimeout — бюджет запроса по умолчанию, если сборка его не задала.
const defaultTimeout = 5 * time.Second

// maxBodyBytes ограничивает тело ответа сервиса, чтобы некорректный ответ не
// читался бесконечно.
const maxBodyBytes = 64 << 10

// Config — параметры адаптера внешнего сервиса проверки.
type Config struct {
	// Endpoint — базовый адрес сервиса проверки. Пусто — сервис не сконфигурирован,
	// и адаптер честно сообщает о недоступности.
	Endpoint string
	// APIKey — ключ доступа к сервису; пусто — заголовок не отправляется.
	APIKey string
	// Client — HTTP-клиент; nil — клиент по умолчанию.
	Client *http.Client
	// Timeout — бюджет одного запроса; 0 — значение по умолчанию.
	Timeout time.Duration
}

// Verifier — адаптер проверки адреса через внешний сервис.
type Verifier struct {
	endpoint string
	apiKey   string
	client   *http.Client
	timeout  time.Duration
}

// Verifier реализует порт внешней верификации.
var _ ports.AddressVerifier = (*Verifier)(nil)

// New создаёт адаптер. Пустой endpoint допустим: адаптер собран, но проверка
// честно недоступна.
func New(cfg Config) *Verifier {
	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Verifier{
		endpoint: strings.TrimSpace(cfg.Endpoint),
		apiKey:   strings.TrimSpace(cfg.APIKey),
		client:   client,
		timeout:  timeout,
	}
}

// providerResponse — ответ сервиса проверки в согласованном формате.
type providerResponse struct {
	Outcome  string `json:"outcome"`
	CatchAll bool   `json:"catch_all"`
	Reason   string `json:"reason"`
	Source   string `json:"source"`
}

// VerifyMailbox выполняет запрос к сервису проверки и нормализует ответ. При
// пустом endpoint, сбое транспорта, не-success статусе или нераспознанном исходе
// возвращается ports.ErrVerifierUnavailable.
func (v *Verifier) VerifyMailbox(ctx context.Context, address string) (ports.MailboxVerdict, error) {
	if v == nil {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: адаптер не собран", ports.ErrVerifierUnavailable)
	}
	if v.endpoint == "" {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: провайдер проверки не сконфигурирован", ports.ErrVerifierUnavailable)
	}
	target := strings.TrimSpace(address)
	if target == "" {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: пустой адрес", ports.ErrVerifierUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	payload, err := json.Marshal(struct {
		Address string `json:"address"`
	}{Address: target})
	if err != nil {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: сборка запроса: %v", ports.ErrVerifierUnavailable, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, v.endpoint, bytes.NewReader(payload))
	if err != nil {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: сборка запроса: %v", ports.ErrVerifierUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if v.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+v.apiKey)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ports.MailboxVerdict{}, err
		}
		return ports.MailboxVerdict{}, fmt.Errorf("%w: запрос: %v", ports.ErrVerifierUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: чтение ответа: %v", ports.ErrVerifierUnavailable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: сервис ответил %d", ports.ErrVerifierUnavailable, resp.StatusCode)
	}

	var decoded providerResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: разбор ответа: %v", ports.ErrVerifierUnavailable, err)
	}
	outcome, ok := normalizeOutcome(decoded.Outcome)
	if !ok {
		return ports.MailboxVerdict{}, fmt.Errorf("%w: нераспознанный исход %q", ports.ErrVerifierUnavailable, decoded.Outcome)
	}
	reason := strings.TrimSpace(decoded.Reason)
	if reason == "" {
		reason = "ответ сервиса проверки адресов"
	}
	source := strings.TrimSpace(decoded.Source)
	if source == "" {
		source = "verification-provider"
	}
	return ports.MailboxVerdict{
		Outcome:  outcome,
		CatchAll: decoded.CatchAll,
		Reason:   reason,
		Source:   source,
	}, nil
}

// normalizeOutcome переводит строковый исход сервиса в согласованный набор.
func normalizeOutcome(raw string) (ports.MailboxOutcome, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ports.MailboxAccepted):
		return ports.MailboxAccepted, true
	case string(ports.MailboxRejected):
		return ports.MailboxRejected, true
	case string(ports.MailboxUnknown):
		return ports.MailboxUnknown, true
	default:
		return "", false
	}
}
