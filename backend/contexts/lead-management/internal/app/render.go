package app

import (
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// VariableOption — переменная каталога переменных: ключ, человекочитаемая подпись
// для интерфейса и описание источника значения. Подписи и источники — данные
// представления, поэтому живут в прикладном слое, а не в домене.
type VariableOption struct {
	// Key — ключ переменной из каталога домена.
	Key string
	// Label — подпись переменной для оператора.
	Label string
	// Source — описание поля-источника значения.
	Source string
}

// variableCatalog — каталог переменных прикладного слоя: для каждого ключа
// домена — подпись и источник. Порядок совпадает с каноническим порядком домена.
var variableCatalog = []VariableOption{
	{Key: string(domain.MergeVariableCompany), Label: "Название компании", Source: "название карточки"},
	{Key: string(domain.MergeVariableCompanyDomain), Label: "Домен компании", Source: "нормализованный идентификатор карточки"},
	{Key: string(domain.MergeVariableCompanyDescription), Label: "Описание компании", Source: "описание карточки"},
	{Key: string(domain.MergeVariableContact), Label: "Роль контакта", Source: "роль контакта"},
	{Key: string(domain.MergeVariableContactEmail), Label: "Email контакта", Source: "адрес контакта"},
	{Key: string(domain.MergeVariableContactPhone), Label: "Телефон контакта", Source: "телефон контакта"},
}

// VariableCatalog возвращает каталог переменных в порядке объявления (копию).
func VariableCatalog() []VariableOption {
	return append([]VariableOption(nil), variableCatalog...)
}

// resolveVariable возвращает значение переменной из карточки компании и контакта.
// Ключ без сопоставленного поля резолвится в пусто — это честное поведение, а не
// выдумка: пустое значение уходит в объявленный fallback.
func resolveVariable(key string, company *domain.Company, contact *domain.Contact) string {
	switch domain.MergeVariableKey(key) {
	case domain.MergeVariableCompany:
		if company != nil {
			return company.Name().Value
		}
	case domain.MergeVariableCompanyDomain:
		if company != nil {
			return company.ID().String()
		}
	case domain.MergeVariableCompanyDescription:
		if company != nil {
			return company.Description().Value
		}
	case domain.MergeVariableContact:
		if contact != nil && contact.Role() != nil {
			return contact.Role().Value
		}
	case domain.MergeVariableContactEmail:
		if contact != nil && contact.Email() != nil {
			return contact.Email().Value
		}
	case domain.MergeVariableContactPhone:
		if contact != nil && contact.Phone() != nil {
			return contact.Phone().Value
		}
	}
	return ""
}

// ComposeEmail собирает тему и тело письма по шаблону библиотеки: значения берутся
// из каталога по объявленным переменным шаблона, а fallback — из объявленной
// переменной. Тема и тело собираются одним разбором плейсхолдеров.
func ComposeEmail(template *domain.EmailTemplate, company *domain.Company, contact *domain.Contact) (subject, body string) {
	return ComposeEmailWithValues(template, company, contact, nil)
}

// ComposeEmailWithValues собирает письмо, позволяя переопределить значения
// объявленных переменных (предпросмотр с правкой оператора). Пустое
// переопределение не отменяет значение каталога: порядок — переопределение,
// значение каталога, объявленный fallback.
func ComposeEmailWithValues(template *domain.EmailTemplate, company *domain.Company, contact *domain.Contact, overrides map[string]string) (subject, body string) {
	if template == nil {
		return "", ""
	}
	fallbacks := make(map[string]string, len(template.Variables()))
	for _, variable := range template.Variables() {
		fallbacks[string(variable.Key)] = variable.Fallback
	}
	resolve := func(field string) string {
		if value := strings.TrimSpace(overrides[field]); value != "" {
			return value
		}
		if value := strings.TrimSpace(resolveVariable(field, company, contact)); value != "" {
			return value
		}
		return fallbacks[field]
	}
	subject = renderMerge(template.Subject(), resolve)
	body = renderMerge(template.Body(), resolve)
	return subject, body
}

// renderMerge заменяет плейсхолдеры {{field}} значениями из resolve. Разбор
// наивный и не поддерживает вложенные фигурные скобки (см. domain.Placeholders):
// необъявленных плейсхолдеров в шаблоне быть не может — это инвариант агрегата.
func renderMerge(template string, resolve func(field string) string) string {
	var builder strings.Builder
	rest := template
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			builder.WriteString(rest)
			return builder.String()
		}
		end := strings.Index(rest[start+2:], "}}")
		if end < 0 {
			builder.WriteString(rest)
			return builder.String()
		}
		end += start + 2
		builder.WriteString(rest[:start])
		field := strings.TrimSpace(rest[start+2 : end])
		builder.WriteString(resolve(field))
		rest = rest[end+2:]
	}
}
