package domain

import (
	"encoding/json"
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

// MarshalJSON сохраняет локаль как каноническую строку "ru-RU"/"ru". Снимок
// состояния версии хранится в JSONB, а у локали закрытые поля, поэтому адаптер
// опирается на этот метод, а не на отражение.
func (l ContentLocale) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

// UnmarshalJSON читает локаль из канонической строки. Пустая строка даёт
// нулевую локаль: отсутствие проверяет восстановление снимка. Допустимость
// языка и региона проверяет NewContentLocale, поэтому испорченный снимок
// отвергается уже на разборе.
func (l *ContentLocale) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		*l = ContentLocale{}
		return nil
	}
	language := raw
	region := ""
	if index := strings.IndexByte(raw, '-'); index >= 0 {
		language, region = raw[:index], raw[index+1:]
	}
	parsed, err := NewContentLocale(language, region)
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}

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
