package components

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	// База часовых поясов встроена в бинарник: локализация «часовых поясов»
	// должна работать одинаково на машине сборки и в минимальном контейнере, где
	// системной базы может не быть.
	_ "time/tzdata"
)

// Formatter приводит даты, числа, валюты и часовые пояса к виду локали. Значения
// отделены от разметки: шаблон показывает готовую строку, поэтому смена локали
// не трогает шаблоны (web-frontend-architecture, срез 7, 7.1; design.md,
// Решение 12).
type Formatter struct {
	locale Locale
}

// NewFormatter возвращает форматтер для локали.
func NewFormatter(locale Locale) Formatter { return Formatter{locale: locale} }

// DefaultFormatter — форматтер локали по умолчанию (ru).
func DefaultFormatter() Formatter { return Formatter{locale: LocaleRu} }

// Date форматирует дату в календарный вид локали: «02.01.2006» для ru.
func (f Formatter) Date(t time.Time) string {
	switch f.locale {
	default:
		return t.Format("02.01.2006")
	}
}

// DateTime форматирует дату и время с точностью до минуты: «02.01.2006 15:04».
func (f Formatter) DateTime(t time.Time) string {
	switch f.locale {
	default:
		return t.Format("02.01.2006 15:04")
	}
}

// TimeZone возвращает имя часового пояса в момент t; при его отсутствии —
// смещение от UTC. Имя пояса не выдумывается.
func (f Formatter) TimeZone(t time.Time) string {
	if name, offset := t.Zone(); name != "" {
		return name
	} else {
		return formatOffset(offset)
	}
}

// DateTimeInZone переводит момент в указанный часовой пояс и форматирует дату,
// время и пояс вместе. Неизвестный пояс — честная ошибка, а не молчаливый откат
// к UTC: неверный пояс в отчёте хуже явного отказа.
func (f Formatter) DateTimeInZone(t time.Time, zone string) (string, error) {
	name := strings.TrimSpace(zone)
	if name == "" {
		local := t.UTC()
		return f.DateTime(local) + " " + f.TimeZone(local), nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return "", fmt.Errorf("components: неизвестный часовой пояс %q: %w", zone, err)
	}
	local := t.In(location)
	return f.DateTime(local) + " " + f.TimeZone(local), nil
}

// Integer форматирует целое число с группировкой разрядов.
func (f Formatter) Integer(n int64) string {
	return f.groupDigits(strconv.FormatInt(n, 10))
}

// Number форматирует число с группировкой разрядов и десятичным разделителем
// локали (запятая для ru). Отрицательное число разрядов после запятой округляет
// до целого.
func (f Formatter) Number(value float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	return f.groupDigits(strconv.FormatFloat(value, 'f', decimals, 64))
}

// Currency форматирует денежную сумму: сумма с двумя разрядами, затем знак
// валюты через неразрывный пробел. Неизвестный код валюты показывается кодом, а
// не выдуманным символом.
func (f Formatter) Currency(amount float64, code string) string {
	upper := strings.ToUpper(strings.TrimSpace(code))
	symbol, ok := currencySymbols[upper]
	if !ok {
		symbol = upper
	}
	return f.Number(amount, 2) + "\u00a0" + symbol
}

// currencySymbols — знаки известных валют. Расширяется по мере надобности;
// неизвестный код показывается как есть.
var currencySymbols = map[string]string{
	"RUB": "₽",
	"USD": "$",
	"EUR": "€",
	"GBP": "£",
}

// groupDigits группирует целую часть по три разряда неразрывным пробелом и
// переводит десятичную точку в запятую. Знак сохраняется.
func (f Formatter) groupDigits(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "−"
		s = s[1:]
	}
	intPart, frac := s, ""
	if index := strings.IndexByte(s, '.'); index >= 0 {
		intPart, frac = s[:index], s[index+1:]
	}
	var builder strings.Builder
	for i := 0; i < len(intPart); i++ {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			builder.WriteRune('\u00a0')
		}
		builder.WriteByte(intPart[i])
	}
	result := sign + builder.String()
	if frac != "" {
		result += "," + frac
	}
	return result
}

// formatOffset форматирует смещение от UTC как «UTC+03:00».
func formatOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	return fmt.Sprintf("UTC%s%02d:%02d", sign, hours, minutes)
}
