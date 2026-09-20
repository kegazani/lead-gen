package web

import (
	"strings"
	"unicode"

	"lead-gen/frontend/components"
	"lead-gen/frontend/public/internal/ports"
)

// maxMessageLen — предел длины сообщения. Ограничение защищает приёмник
// владельца от заявки-«простыни» и не является правилом бизнеса.
const maxMessageLen = 2000

// validateSubmission проверяет заполнение формы. Это гигиена публичной формы, а
// не доменное правило: edge не решает, годится ли заявка бизнесу — это дело
// владельца. Отвергается только заведомо неполное и неконтактное значение, а
// также заявка без согласия на обработку персональных данных.
//
// Возвращается карта «поле → сообщение»: шаблон показывает ошибку рядом с
// полем, а не одной общей строкой.
func validateSubmission(submission ports.Submission) map[string]string {
	problems := make(map[string]string)
	if submission.Name == "" {
		problems["name"] = "Укажите имя"
	}
	switch {
	case submission.Contact == "":
		problems["contact"] = "Укажите почту или телефон"
	case !looksLikeContact(submission.Contact):
		problems["contact"] = "Похоже, это не почта и не телефон"
	}
	if len([]rune(submission.Message)) > maxMessageLen {
		problems["message"] = "Сообщение длиннее 2000 символов"
	}
	if !submission.Consent {
		problems["consent"] = "Без согласия на обработку данных заявка не принимается"
	}
	return problems
}

// looksLikeContact принимает почту или телефон в свободной форме. Строгая
// проверка адреса принадлежит владельцу — здесь отвергается только значение,
// которое не является ни почтой, ни телефоном ни при какой трактовке.
func looksLikeContact(value string) bool {
	if strings.Contains(value, "@") {
		local, domain, found := strings.Cut(value, "@")
		if !found || local == "" || domain == "" {
			return false
		}
		if strings.Contains(domain, "@") {
			return false
		}
		return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
	}
	digits := 0
	for _, symbol := range value {
		if unicode.IsDigit(symbol) {
			digits++
		}
	}
	return digits >= 7
}

// valuesOf переносит заявку в модель разметки, чтобы вернуть введённое при
// ошибке. Пароля и других секретов в форме нет, поэтому возвращать можно всё.
func valuesOf(submission ports.Submission) formValues {
	return formValues{
		Name:    submission.Name,
		Contact: submission.Contact,
		Message: submission.Message,
		Consent: submission.Consent,
	}
}

// formFields собирает поля формы для разметки и разбирает ошибки валидации в
// представление: каждая ошибка ложится к своему полю через components.FieldError.
// Порядок полей задаёт порядок на странице: имя, контакт, сообщение, согласие.
func formFields(values formValues, problems map[string]string) []components.Field {
	return []components.Field{
		{
			ID: "name", Name: "name", Label: "Имя",
			Value: values.Name, Autocomplete: "name", Required: true,
			Error: components.FieldError(problems, "name"),
		},
		{
			ID: "contact", Name: "contact", Label: "Почта или телефон",
			Value: values.Contact, Autocomplete: "email", Required: true,
			Error: components.FieldError(problems, "contact"),
		},
		{
			ID: "message", Name: "message", Label: "Сообщение", Type: "textarea",
			Rows: 5, Value: values.Message,
			Error: components.FieldError(problems, "message"),
		},
		{
			Name: "consent", Label: "Согласен на обработку персональных данных",
			Type: "checkbox", Class: "consent", Checked: values.Consent,
			Error: components.FieldError(problems, "consent"),
		},
	}
}
