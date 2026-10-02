package domain

import (
	"fmt"
	"strings"
	"time"
)

// AttemptOutcome — нормализованный исход попытки доставки.
//
// Набор закрыт: сырой ответ провайдера сводится к одному из объявленных
// значений до того, как попадёт в домен, — так выполняется инвариант
// sender-reputation «ограничения вычисляются только по нормализованным ответам».
type AttemptOutcome string

const (
	// OutcomeDelivered — провайдер принял сообщение.
	OutcomeDelivered AttemptOutcome = "delivered"

	// OutcomeHardBounce — постоянный отказ (адреса нет, домен отвергает).
	OutcomeHardBounce AttemptOutcome = "hard-bounce"

	// OutcomeSoftBounce — временный отказ (ящик полон, отложено).
	OutcomeSoftBounce AttemptOutcome = "soft-bounce"

	// OutcomeTemporary — сбой транспорта или недоступность провайдера; не
	// является нормализованным ответом провайдера и в репутацию не входит.
	OutcomeTemporary AttemptOutcome = "temporary"

	// OutcomeSuppressed — попытка заблокирована стоп-листом непосредственно
	// перед доставкой. Не является ответом провайдера.
	OutcomeSuppressed AttemptOutcome = "suppressed"
)

// IsProviderResponse сообщает, является ли исход нормализованным ответом
// провайдера. Только такие исходы учитываются в репутации домена.
func (o AttemptOutcome) IsProviderResponse() bool {
	switch o {
	case OutcomeDelivered, OutcomeHardBounce, OutcomeSoftBounce:
		return true
	default:
		return false
	}
}

// isValid сообщает, что исход объявлен доменом.
func (o AttemptOutcome) isValid() bool {
	switch o {
	case OutcomeDelivered, OutcomeHardBounce, OutcomeSoftBounce, OutcomeTemporary, OutcomeSuppressed:
		return true
	default:
		return false
	}
}

// DeliveryAttempt — value object: номер попытки доставки и результат ответа
// провайдера (architecture/domain-catalog.yaml, value_objects.delivery-attempt).
type DeliveryAttempt struct {
	// Number — номер попытки, начиная с 1. Назначается агрегатом, а не
	// вызывающим: нумерация — часть инварианта сообщения.
	Number int

	// Outcome — нормализованный исход попытки.
	Outcome AttemptOutcome

	// ProviderCode — нормализованный код ответа провайдера (необязателен).
	ProviderCode string

	// At — момент получения исхода.
	At time.Time
}

func (a DeliveryAttempt) validate() error {
	if a.Number <= 0 {
		return fmt.Errorf("%w: %d", ErrInvalidAttemptNumber, a.Number)
	}
	if !a.Outcome.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownAttemptOutcome, a.Outcome)
	}
	if a.At.IsZero() {
		return ErrAttemptTimeRequired
	}
	return nil
}

// NormalizedOutcome — нормализованный ответ провайдера, пригодный для учёта в
// репутации домена. Тип отдельный от DeliveryAttempt, чтобы репутацию нельзя
// было обновить исходом, не являющимся ответом провайдера.
type NormalizedOutcome struct {
	// Outcome — нормализованный исход провайдера.
	Outcome AttemptOutcome

	// ProviderCode — нормализованный код ответа.
	ProviderCode string
}

// NormalizeProviderResponse сводит исход попытки к нормализованному ответу
// провайдера. Второе значение равно false, если исход не является ответом
// провайдера (сбой транспорта, стоп-лист): такой исход в репутацию не входит.
func NormalizeProviderResponse(outcome AttemptOutcome, providerCode string) (NormalizedOutcome, bool) {
	if !outcome.IsProviderResponse() {
		return NormalizedOutcome{}, false
	}
	return NormalizedOutcome{
		Outcome:      outcome,
		ProviderCode: strings.TrimSpace(providerCode),
	}, true
}
