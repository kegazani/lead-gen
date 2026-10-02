package domain

import (
	"fmt"
	"strings"
)

// DeliveryAddress — value object: проверенный адрес назначения сообщения.
//
// Неизменяем; создать значение можно только через ParseDeliveryAddress, поэтому
// произвольная строка в домен не попадает. Адрес нормализуется (регистр домена
// приводится к нижнему, окружающие пробелы снимаются), чтобы один получатель не
// давал двух разных значений.
type DeliveryAddress struct {
	value string
}

// ParseDeliveryAddress разбирает и нормализует адрес назначения. Проверяется
// базовая форма почтового адреса: ровно один "@", непустые локальная часть и
// домен, домен содержит точку, пробелы внутри запрещены.
func ParseDeliveryAddress(raw string) (DeliveryAddress, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DeliveryAddress{}, ErrEmptyDeliveryAddress
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return DeliveryAddress{}, fmt.Errorf("%w: %q", ErrInvalidDeliveryAddress, raw)
	}
	at := strings.IndexByte(trimmed, '@')
	if at <= 0 || at != strings.LastIndexByte(trimmed, '@') || at == len(trimmed)-1 {
		return DeliveryAddress{}, fmt.Errorf("%w: %q", ErrInvalidDeliveryAddress, raw)
	}
	local := trimmed[:at]
	domainPart := strings.ToLower(trimmed[at+1:])
	if local == "" || !strings.Contains(domainPart, ".") || strings.HasPrefix(domainPart, ".") || strings.HasSuffix(domainPart, ".") {
		return DeliveryAddress{}, fmt.Errorf("%w: %q", ErrInvalidDeliveryAddress, raw)
	}
	return DeliveryAddress{value: local + "@" + domainPart}, nil
}

// String возвращает нормализованный адрес.
func (a DeliveryAddress) String() string { return a.value }

// IsZero сообщает, что адрес не задан.
func (a DeliveryAddress) IsZero() bool { return a.value == "" }

// Equals сравнивает адреса.
func (a DeliveryAddress) Equals(other DeliveryAddress) bool { return a.value == other.value }

// validate проверяет, что значение собрано конструктором.
func (a DeliveryAddress) validate() error {
	if a.IsZero() {
		return ErrEmptyDeliveryAddress
	}
	return nil
}
