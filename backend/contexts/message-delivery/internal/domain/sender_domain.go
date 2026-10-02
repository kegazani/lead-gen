package domain

import (
	"fmt"
	"strings"
)

// SenderDomain — value object: отправляющий домен, репутация которого
// учитывается при отправке.
//
// Неизменяем; создать значение можно только через ParseSenderDomain, поэтому
// произвольная строка в домен не попадает. Домен нормализуется к нижнему
// регистру, чтобы один домен не давал двух значений репутации.
type SenderDomain struct {
	value string
}

// ParseSenderDomain разбирает и нормализует отправляющий домен. Проверяется
// базовая форма доменного имени: непустое, содержит точку, без пробелов и
// ведущей/замыкающей точки.
func ParseSenderDomain(raw string) (SenderDomain, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return SenderDomain{}, ErrEmptySenderDomain
	}
	if strings.ContainsAny(trimmed, " \t\r\n") ||
		!strings.Contains(trimmed, ".") ||
		strings.HasPrefix(trimmed, ".") ||
		strings.HasSuffix(trimmed, ".") {
		return SenderDomain{}, fmt.Errorf("%w: %q", ErrInvalidSenderDomain, raw)
	}
	return SenderDomain{value: trimmed}, nil
}

// String возвращает нормализованный домен.
func (d SenderDomain) String() string { return d.value }

// IsZero сообщает, что домен не задан.
func (d SenderDomain) IsZero() bool { return d.value == "" }

// Equals сравнивает домены.
func (d SenderDomain) Equals(other SenderDomain) bool { return d.value == other.value }

// validate проверяет, что значение собрано конструктором.
func (d SenderDomain) validate() error {
	if d.IsZero() {
		return ErrEmptySenderDomain
	}
	return nil
}
