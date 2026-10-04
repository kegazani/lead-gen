package domain

import (
	"fmt"
	"strings"
)

// Форма заявки лендинга (capability landing-page-generator, требование «Форма
// заявки с передачей в контур клиента»).
//
// Состав полей ограничен минимально необходимым для связи: имя и хотя бы один
// контакт (почта или телефон) плюс необязательное сообщение. Форма передаёт
// данные в приёмник в контуре клиента; платформа заявку не сохраняет, поэтому
// адрес приёмника обязателен — без него форме некуда отправлять.

// LeadFormField — поле формы заявки.
type LeadFormField string

const (
	// LeadFormFieldName — имя заявителя.
	LeadFormFieldName LeadFormField = "name"

	// LeadFormFieldEmail — адрес электронной почты заявителя.
	LeadFormFieldEmail LeadFormField = "email"

	// LeadFormFieldPhone — телефон заявителя.
	LeadFormFieldPhone LeadFormField = "phone"

	// LeadFormFieldMessage — необязательное сообщение заявителя.
	LeadFormFieldMessage LeadFormField = "message"
)

// leadFormFieldOrder — согласованный порядок полей формы.
func leadFormFieldOrder() []LeadFormField {
	return []LeadFormField{LeadFormFieldName, LeadFormFieldEmail, LeadFormFieldPhone, LeadFormFieldMessage}
}

func (f LeadFormField) isValid() bool {
	switch f {
	case LeadFormFieldName, LeadFormFieldEmail, LeadFormFieldPhone, LeadFormFieldMessage:
		return true
	default:
		return false
	}
}

// Label возвращает подпись поля на русском языке.
func (f LeadFormField) Label() string {
	switch f {
	case LeadFormFieldName:
		return "Имя"
	case LeadFormFieldEmail:
		return "Электронная почта"
	case LeadFormFieldPhone:
		return "Телефон"
	case LeadFormFieldMessage:
		return "Сообщение"
	default:
		return string(f)
	}
}

// LeadForm — форма заявки: адрес приёмника клиента и минимальный состав полей.
type LeadForm struct {
	action string
	fields []LeadFormField
}

// NewLeadForm собирает форму заявки. Адрес приёмника обязателен. Состав полей
// обязан включать имя и хотя бы один контакт, не содержать неизвестных и
// повторяющихся полей, и быть подмножеством допустимых.
func NewLeadForm(action string, fields []LeadFormField) (LeadForm, error) {
	trimmedAction := strings.TrimSpace(action)
	if trimmedAction == "" {
		return LeadForm{}, ErrFormActionRequired
	}
	if !isSafeFormAction(trimmedAction) {
		return LeadForm{}, fmt.Errorf("%w: адрес приёмника %q", ErrInvalidLeadForm, action)
	}
	if len(fields) == 0 {
		return LeadForm{}, fmt.Errorf("%w: форма без полей", ErrInvalidLeadForm)
	}
	seen := make(map[LeadFormField]bool, len(fields))
	hasName := false
	hasContact := false
	for _, field := range fields {
		if !field.isValid() {
			return LeadForm{}, fmt.Errorf("%w: неизвестное поле %q", ErrInvalidLeadForm, field)
		}
		if seen[field] {
			return LeadForm{}, fmt.Errorf("%w: повтор поля %q", ErrInvalidLeadForm, field)
		}
		seen[field] = true
		switch field {
		case LeadFormFieldName:
			hasName = true
		case LeadFormFieldEmail, LeadFormFieldPhone:
			hasContact = true
		}
	}
	if !hasName || !hasContact {
		return LeadForm{}, fmt.Errorf("%w: нужны имя и хотя бы один контакт", ErrInvalidLeadForm)
	}
	return LeadForm{action: trimmedAction, fields: orderedFields(fields)}, nil
}

// Action возвращает адрес приёмника заявок в контуре клиента.
func (f LeadForm) Action() string { return f.action }

// Fields возвращает поля формы в согласованном порядке.
func (f LeadForm) Fields() []LeadFormField { return append([]LeadFormField(nil), f.fields...) }

// IsZero сообщает, что форма не задана.
func (f LeadForm) IsZero() bool { return f.action == "" }

// isSafeFormAction сообщает, что адрес приёмника безопасен как атрибут action:
// без управляющих символов, пробелов и символов разметки, не с опасной схемой.
func isSafeFormAction(action string) bool {
	lower := strings.ToLower(action)
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") {
		return false
	}
	for _, r := range action {
		switch r {
		case ' ', '\t', '\n', '\r', '<', '>', '"', '\'', '`':
			return false
		}
		if r < 0x20 {
			return false
		}
	}
	return true
}

// orderedFields возвращает поля в согласованном порядке, сохраняя только
// выбранные.
func orderedFields(fields []LeadFormField) []LeadFormField {
	chosen := make(map[LeadFormField]bool, len(fields))
	for _, field := range fields {
		chosen[field] = true
	}
	out := make([]LeadFormField, 0, len(fields))
	for _, field := range leadFormFieldOrder() {
		if chosen[field] {
			out = append(out, field)
		}
	}
	return out
}
