package domain

import (
	"fmt"
	"strings"
)

// ContentLocale — value object content-locale: язык и регион, определяющие
// правила подготовки контента.
//
// Язык обязателен и задаётся буквами (ISO 639, например "ru"); регион
// необязателен и задаётся буквами или цифрами (ISO 3166, например "RU"). Значения
// неизменяемы, поля закрыты.
type ContentLocale struct {
	language string
	region   string
}

// NewContentLocale собирает локаль из языка и региона. Пустой язык — ошибка; при
// непустом регионе регион должен быть допустимым.
func NewContentLocale(language, region string) (ContentLocale, error) {
	lang := strings.TrimSpace(language)
	if lang == "" {
		return ContentLocale{}, fmt.Errorf("%w: язык не задан", ErrInvalidLocale)
	}
	if !isAlpha(lang) {
		return ContentLocale{}, fmt.Errorf("%w: язык %q содержит недопустимые символы", ErrInvalidLocale, language)
	}
	reg := strings.TrimSpace(region)
	if reg != "" && !isAlphaNumeric(reg) {
		return ContentLocale{}, fmt.Errorf("%w: регион %q содержит недопустимые символы", ErrInvalidLocale, region)
	}
	return ContentLocale{
		language: strings.ToLower(lang),
		region:   strings.ToUpper(reg),
	}, nil
}

// Language возвращает код языка в нижнем регистре.
func (l ContentLocale) Language() string { return l.language }

// Region возвращает код региона в верхнем регистре (может быть пустым).
func (l ContentLocale) Region() string { return l.region }

// String возвращает каноническую запись локали: "ru-RU" или "ru".
func (l ContentLocale) String() string {
	if l.region == "" {
		return l.language
	}
	return l.language + "-" + l.region
}

// IsZero сообщает, что локаль не задана.
func (l ContentLocale) IsZero() bool { return l.language == "" }

// isAlpha проверяет, что строка состоит только из латинских букв.
func isAlpha(value string) bool {
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return value != ""
}

// isAlphaNumeric проверяет, что строка состоит только из латинских букв и цифр.
func isAlphaNumeric(value string) bool {
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return value != ""
}
