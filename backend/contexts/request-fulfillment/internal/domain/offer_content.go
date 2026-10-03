package domain

import (
	"encoding/json"
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

// MarshalJSON сохраняет VO как JSON-строку. Снимок состояния (RequestSnapshot)
// сериализуется целиком, а у VO поле закрыто; без явного метода текст оффера
// потерялся бы при записи снимка в хранилище.
func (c OfferContent) MarshalJSON() ([]byte, error) { return json.Marshal(c.text) }

// UnmarshalJSON читает VO из строки. Непустоту текста проверяет восстановление
// оффера (rehydrateOffer), а не сам разбор.
func (c *OfferContent) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	c.text = text
	return nil
}

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

// MarshalJSON сохраняет VO как JSON-строку: у цели закрытое поле, и при записи
// снимка агрегата её описание иначе потерялось бы.
func (t RequestTarget) MarshalJSON() ([]byte, error) { return json.Marshal(t.description) }

// UnmarshalJSON читает VO из строки. Пустая цель допустима.
func (t *RequestTarget) UnmarshalJSON(data []byte) error {
	var description string
	if err := json.Unmarshal(data, &description); err != nil {
		return err
	}
	t.description = description
	return nil
}

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
