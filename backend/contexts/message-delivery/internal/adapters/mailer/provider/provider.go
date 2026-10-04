// Package provider реализует ports.Mailer поверх контракта mail
// (lead-gen/contracts/integration-platform/mail): реальная отправка выполняется
// провайдером владельца integration-platform (SMTP-адаптер), а контекст
// message-delivery держит только форму порта и не импортирует чужие внутренние
// пакеты.
//
// Нормализация ответа. Успешная отправка переводится в нормализованный исход
// delivered с кодом принятия. Ответ SMTP-сервера (протокольная ошибка)
// нормализуется по коду: 4xx — мягкий отказ (soft-bounce), 5xx — жёсткий
// (hard-bounce). Сбой транспорта (соединение, отмена, ошибка протокола без кода)
// не является решением провайдера о получателе и возвращается как
// ports.ErrMailerUnavailable — временный исход.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"strconv"
	"strings"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/integration-platform/mail"
)

// defaultSuccessCode — нормализованный код успешного ответа, если вызывающий не
// задал свой: SMTP-принятие 250.
const defaultSuccessCode = "250"

// Config — параметры адаптера.
type Config struct {
	// Port — порт отправки владельца integration-platform. Обязателен.
	Port mail.Port

	// From — адрес отправителя писем. Обязателен: провайдеру нужен конверт.
	From string

	// SuccessCode — нормализованный код успешного ответа. Пусто — "250".
	SuccessCode string
}

// Mailer — реализация ports.Mailer поверх контракта mail.
type Mailer struct {
	port        mail.Port
	from        string
	successCode string
}

// Mailer реализует порт отправки почты.
var _ ports.Mailer = (*Mailer)(nil)

// New собирает адаптер. Отсутствие порта или адреса отправителя — ошибка
// конфигурации, а не молчаливый отказ на пути отправки.
func New(cfg Config) (*Mailer, error) {
	if cfg.Port == nil {
		return nil, errors.New("provider: порт mail обязателен")
	}
	from := strings.TrimSpace(cfg.From)
	if from == "" {
		return nil, errors.New("provider: адрес отправителя обязателен")
	}
	code := strings.TrimSpace(cfg.SuccessCode)
	if code == "" {
		code = defaultSuccessCode
	}
	return &Mailer{port: cfg.Port, from: from, successCode: code}, nil
}

// normalizeError переводит ошибку контракта mail в ответ порта отправки.
// Протокольный код 4xx — мягкий отказ, 5xx — жёсткий. Ошибки транспорта
// (соединение, отмена, протокол без кода) и ошибки аутентификации/доступа
// (диапазон 530–539) — не решение о получателе, а сбой окружения, и не должны
// менять репутацию домена: они возвращаются как ports.ErrMailerUnavailable.
func normalizeError(err error) (ports.ProviderResponse, error) {
	code, ok := providerCode(err)
	if !ok || (code >= 530 && code < 540) {
		return ports.ProviderResponse{}, fmt.Errorf("%w: %v", ports.ErrMailerUnavailable, err)
	}
	outcome := domain.OutcomeHardBounce
	if code >= 400 && code < 500 {
		outcome = domain.OutcomeSoftBounce
	}
	return ports.ProviderResponse{Outcome: outcome, ProviderCode: strconv.Itoa(code)}, nil
}

// Send реализует ports.Mailer. Тема и тело письма передаются провайдеру.
func (m *Mailer) Send(ctx context.Context, message ports.OutgoingMessage) (ports.ProviderResponse, error) {
	err := m.port.Send(ctx, mail.Message{
		From:    m.from,
		To:      message.To.String(),
		Subject: message.Subject,
		Body:    message.Body,
	})
	if err == nil {
		return ports.ProviderResponse{Outcome: domain.OutcomeDelivered, ProviderCode: m.successCode}, nil
	}
	return normalizeError(err)
}

// providerCode извлекает код протокольного ответа SMTP, если ошибка его несёт.
// Отсутствие кода означает сбой транспорта, а не решение провайдера.
func providerCode(err error) (int, bool) {
	var protoErr *textproto.Error
	if errors.As(err, &protoErr) && protoErr.Code > 0 {
		return protoErr.Code, true
	}
	return 0, false
}
