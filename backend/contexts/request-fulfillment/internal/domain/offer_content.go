package domain

import (
	"fmt"
	"strings"
)

// OfferContent — текст оффера (VO offer-content): что клиент предлагает и кому.
// Неизменяем и непуст.
type OfferContent struct {
	text string
}

// NewOfferContent собирает VO из сырого текста, обрезая краевые пробелы.
// Пустой текст отвергается: заявка без оффера не создаётся.
func NewOfferContent(text string) (OfferContent, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return OfferContent{}, ErrEmptyOfferContent
	}
	return OfferContent{text: trimmed}, nil
}

// Text возвращает нормализованный текст оффера.
func (c OfferContent) Text() string { return c.text }

// IsZero сообщает, что текст оффера пуст.
func (c OfferContent) IsZero() bool { return strings.TrimSpace(c.text) == "" }

// checkVersioned проверяет сохранённое значение VO при восстановлении.
func (c OfferContent) checkVersioned() error {
	if c.IsZero() {
		return ErrEmptyOfferContent
	}
	return nil
}

// String возвращает текст оффера.
func (c OfferContent) String() string { return c.text }

// RequestTarget — цель заявки (VO request-target): кому и сколько нужно.
// Отличия от оффера: оффер — что предлагаем, цель — кому и в каком объёме.
// Структура полей (ICP, целевое число лидов, ниши) финализируется отдельным
// change; до этого цель хранится как свободный текст и может быть пустой.
type RequestTarget struct {
	description string
}

// NewRequestTarget нормализует цель заявки. Пустая цель допустима.
func NewRequestTarget(description string) RequestTarget {
	return RequestTarget{description: strings.TrimSpace(description)}
}

// Description возвращает описание цели.
func (t RequestTarget) Description() string { return t.description }

// IsZero сообщает, что цель не задана.
func (t RequestTarget) IsZero() bool { return t.description == "" }

// String возвращает описание цели.
func (t RequestTarget) String() string { return t.description }

// StringOr сообщает описание цели либо запасное значение, если цель не задана.
func (t RequestTarget) StringOr(fallback string) string {
	if t.IsZero() {
		return fallback
	}
	return t.description
}

// describe используется в сообщениях об ошибках.
func (t RequestTarget) describe() string {
	if t.IsZero() {
		return "цель не задана"
	}
	return fmt.Sprintf("цель %q", t.description)
}
