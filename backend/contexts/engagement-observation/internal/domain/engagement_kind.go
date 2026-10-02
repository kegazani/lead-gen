package domain

import (
	"fmt"
	"strings"
)

// EngagementKind — value object engagement-kind: классифицированный тип
// наблюдаемого взаимодействия. Набор замкнут: вид либо объявлен доменом, либо
// сигнал не квалифицируется.
type EngagementKind string

const (
	// KindDelivery — событие доставки письма (исход delivered или bounced).
	KindDelivery EngagementKind = "delivery"

	// KindTransition — переход получателя по трекинг-ссылке.
	KindTransition EngagementKind = "transition"

	// KindReply — ответ получателя на письмо.
	KindReply EngagementKind = "reply"
)

// pixelKinds — подписи, которые распознаются как трекинговый пиксель. Открытие
// письма измеряется пикселем, а policy pixel-free-observation такие сигналы не
// квалифицирует: платформа не собирает метрики открытий по построению.
var pixelKinds = map[string]struct{}{
	"open":         {},
	"opened":       {},
	"opens":        {},
	"pixel":        {},
	"pixels":       {},
	"view":         {},
	"viewed":       {},
	"views":        {},
	"impression":   {},
	"impressions":  {},
	"read":         {},
	"read-receipt": {},
}

// ParseEngagementKind квалифицирует подпись вида взаимодействия. Регистр,
// окружающие пробелы, дефисы и подчёркивания не значимы. Пиксельный вид
// отвергается ErrPixelBasedKind, прочий неизвестный — ErrUnknownKind.
func ParseEngagementKind(value string) (EngagementKind, error) {
	normalized := normalizeToken(value)
	if _, ok := pixelKinds[normalized]; ok {
		return "", fmt.Errorf("%w: %q", ErrPixelBasedKind, value)
	}
	switch EngagementKind(normalized) {
	case KindDelivery, KindTransition, KindReply:
		return EngagementKind(normalized), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownKind, value)
	}
}

// isValid сообщает, что вид объявлен доменом.
func (k EngagementKind) isValid() bool {
	switch k {
	case KindDelivery, KindTransition, KindReply:
		return true
	default:
		return false
	}
}

// IsPixelFree сообщает, что вид свободен от пикселей. Агрегат никогда не хранит
// пиксельный вид, но метод делает запрет явным и проверяемым.
func (k EngagementKind) IsPixelFree() bool { return k.isValid() }

// String возвращает каноническую подпись вида.
func (k EngagementKind) String() string { return string(k) }

// normalizeToken приводит подпись к канонической форме: нижний регистр, единый
// разделитель-дефис, без краевых пробелов.
func normalizeToken(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	return normalized
}
