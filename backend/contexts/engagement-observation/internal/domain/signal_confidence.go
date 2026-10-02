package domain

import "fmt"

// Outcome — исход квалифицированного взаимодействия. Для вида доставки исход
// обязателен (delivered или bounced); для перехода и ответа не названный исход
// заменяется значением по умолчанию.
type Outcome string

const (
	// OutcomeDelivered — письмо доставлено.
	OutcomeDelivered Outcome = "delivered"

	// OutcomeBounced — доставка отвергнута (bounce, отказ).
	OutcomeBounced Outcome = "bounced"

	// OutcomeTransitioned — переход по ссылке состоялся.
	OutcomeTransitioned Outcome = "transitioned"

	// OutcomeReplied — получатель ответил.
	OutcomeReplied Outcome = "replied"
)

// ParseOutcome квалифицирует исход в рамках вида взаимодействия. Исход вне
// закрытого набора вида отвергается ErrUnknownOutcome: выдумывать исход
// прикладной слой не вправе.
func ParseOutcome(kind EngagementKind, value string) (Outcome, error) {
	normalized := normalizeToken(value)
	switch kind {
	case KindDelivery:
		switch normalized {
		case "delivered", "delivery":
			return OutcomeDelivered, nil
		case "bounced", "bounce", "undelivered", "failed":
			return OutcomeBounced, nil
		default:
			return "", fmt.Errorf("%w: доставка %q", ErrUnknownOutcome, value)
		}
	case KindTransition:
		switch normalized {
		case "", "transitioned", "transition", "clicked", "click":
			return OutcomeTransitioned, nil
		default:
			return "", fmt.Errorf("%w: переход %q", ErrUnknownOutcome, value)
		}
	case KindReply:
		switch normalized {
		case "", "replied", "reply", "answered":
			return OutcomeReplied, nil
		default:
			return "", fmt.Errorf("%w: ответ %q", ErrUnknownOutcome, value)
		}
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
}

// String возвращает каноническую подпись исхода.
func (o Outcome) String() string { return string(o) }

// SignalConfidence — value object signal-confidence: степень уверенности в
// квалификации сигнала взаимодействия. Набор замкнут.
type SignalConfidence string

const (
	// ConfidenceHigh — исход подтверждён самим источником (провайдер, webhook).
	ConfidenceHigh SignalConfidence = "high"

	// ConfidenceMedium — исход выведен из нормализованного события доставки.
	ConfidenceMedium SignalConfidence = "medium"

	// ConfidenceLow — исход выведен из менее надёжного контекста.
	ConfidenceLow SignalConfidence = "low"
)

// ParseSignalConfidence разбирает уверенность. Пустое значение заменяется
// значением, выведенным из источника (см. ConfidenceForSource).
func ParseSignalConfidence(value string) (SignalConfidence, error) {
	switch SignalConfidence(normalizeToken(value)) {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return SignalConfidence(normalizeToken(value)), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownConfidence, value)
	}
}

// ConfidenceForSource выводит уверенность по надёжности источника сигнала.
// Провайдер и публичный webhook подтверждают факт сами; нормализованное событие
// доставки из брокера менее прямо; прочее считается низкой уверенностью.
func ConfidenceForSource(source string) SignalConfidence {
	switch normalizeToken(source) {
	case "provider", "provider-webhook", "webhook", "smtp", "esp":
		return ConfidenceHigh
	case "message-delivery", "delivery-event", "broker", "email":
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

// String возвращает каноническую подпись уверенности.
func (c SignalConfidence) String() string { return string(c) }

// isValid сообщает, что уверенность объявлена доменом.
func (c SignalConfidence) isValid() bool {
	switch c {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return true
	default:
		return false
	}
}
