package components

import "strings"

// TruncateText ограничивает текст указанным числом Unicode code points. Если
// строка длиннее, после предела добавляется многоточие; исходная строка не меняется.
func TruncateText(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimRight(string(runes[:limit]), " \t\r\n") + "…"
}
