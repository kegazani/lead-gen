package app

import (
	"net"
	"strconv"
	"strings"
)

// NormalizeDomain приводит домен (или URL) к канонической форме — идентификатору
// компании: нижний регистр, без схемы, префикса www, пути и завершающей точки.
// Порт хоста (host:port) отбрасывается.
func NormalizeDomain(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "//")
	// Отсекаем путь, запрос и фрагмент.
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// Отбрасываем порт, если он есть.
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimSuffix(s, ".")
	return s
}

// NormalizeEmail приводит адрес к канонической форме: нижний регистр, без
// ведущих и завершающих пробелов.
func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// NormalizePhone оставляет только цифры телефона.
func NormalizePhone(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DecodeCloudflareEmail декодирует обфусцированный Cloudflare адрес
// (hex-строка из data-cfemail / __cf_email__). Первый байт — ключ XOR; каждый
// последующий байт расшифровывается операцией XOR с ключом. Возвращает адрес и
// признак успеха (false на некорректной hex-строке).
func DecodeCloudflareEmail(encoded string) (string, bool) {
	encoded = strings.TrimSpace(encoded)
	if len(encoded) < 2 || len(encoded)%2 != 0 {
		return "", false
	}
	key, err := strconv.ParseUint(encoded[:2], 16, 8)
	if err != nil {
		return "", false
	}
	out := make([]byte, 0, len(encoded)/2-1)
	for i := 2; i < len(encoded); i += 2 {
		b, err := strconv.ParseUint(encoded[i:i+2], 16, 8)
		if err != nil {
			return "", false
		}
		out = append(out, byte(b^key))
	}
	return string(out), true
}
