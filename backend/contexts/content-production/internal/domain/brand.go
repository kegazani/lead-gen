package domain

import (
	"fmt"
	"strings"
)

// BrandKit — value object бренд-комплекта клиента: основные цвета, семейство
// шрифтов и текстовый логотип. Применяется при сборке страницы лендинга
// (capability landing-page-generator, задача 7.4).
//
// Комплект необязателен: нулевое значение означает «комплект не предоставлен» и
// влечёт нейтральное оформление. Заданный цвет обязан быть шестнадцатеричным
// (#rrggbb); семейство шрифтов задаётся как готовый CSS-список.
type BrandKit struct {
	primaryColor string
	accentColor  string
	fontFamily   string
	logoText     string
}

// NewBrandKit собирает бренд-комплект. Пустые значения допустимы (поле не
// предоставлено); непустой цвет обязан быть вида #rrggbb.
func NewBrandKit(primaryColor, accentColor, fontFamily, logoText string) (BrandKit, error) {
	primary, err := normalizeColor("основной цвет", primaryColor)
	if err != nil {
		return BrandKit{}, err
	}
	accent, err := normalizeColor("акцентный цвет", accentColor)
	if err != nil {
		return BrandKit{}, err
	}
	font := strings.TrimSpace(fontFamily)
	if !isSafeFontFamily(font) {
		return BrandKit{}, fmt.Errorf("%w: семейство шрифтов %q", ErrInvalidBrandKit, fontFamily)
	}
	return BrandKit{
		primaryColor: primary,
		accentColor:  accent,
		fontFamily:   font,
		logoText:     strings.TrimSpace(logoText),
	}, nil
}

// PrimaryColor возвращает основной цвет или пустую строку.
func (b BrandKit) PrimaryColor() string { return b.primaryColor }

// AccentColor возвращает акцентный цвет или пустую строку.
func (b BrandKit) AccentColor() string { return b.accentColor }

// FontFamily возвращает CSS-список семейств шрифтов или пустую строку.
func (b BrandKit) FontFamily() string { return b.fontFamily }

// LogoText возвращает текстовый логотип или пустую строку.
func (b BrandKit) LogoText() string { return b.logoText }

// IsZero сообщает, что комплект не предоставлен: применяется нейтральное
// оформление.
func (b BrandKit) IsZero() bool {
	return b.primaryColor == "" && b.accentColor == "" && b.fontFamily == "" && b.logoText == ""
}

// normalizeColor проверяет и канонизирует цвет: пусто — допустимо, иначе
// обязательно #rrggbb в нижнем регистре.
func normalizeColor(field, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) != 7 || trimmed[0] != '#' || !isHexTriplet(trimmed[1:]) {
		return "", fmt.Errorf("%w: %s %q", ErrInvalidBrandKit, field, value)
	}
	return strings.ToLower(trimmed), nil
}

// isSafeFontFamily сообщает, что значение семейства шрифтов безопасно вставлять
// в CSS: разрешены буквы, цифры и символы, встречающиеся в списке семейств, — без
// символов, которыми можно закрыть объявление или открыть новое правило.
func isSafeFontFamily(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ' || r == ',' || r == '-' || r == '_' || r == '\'' || r == '"' || r == '.':
		case r > 0x7f:
			// Буквы не-латиницы (например, кириллица) в именах шрифтов допустимы.
		default:
			return false
		}
	}
	return true
}

// isHexTriplet сообщает, что строка — шесть шестнадцатеричных цифр.
func isHexTriplet(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return len(value) == 6
}
