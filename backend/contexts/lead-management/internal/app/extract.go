package app

import (
	"regexp"
	"strings"
)

// Регулярные выражения дешёвых слоёв экстракции. Скомпилированы один раз —
// экстракция вызывается на каждой обойдённой странице.
var (
	// emailRe — адреса электронной почты.
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	// phoneRe — кандидаты телефонов: необязательный «+», затем цифры и
	// разделители (пробел, точка, дефис, скобки).
	phoneRe = regexp.MustCompile(`\+?[0-9(][0-9 ()\-.]{5,}[0-9]`)
	// dateRe — узнаваемые формы даты: ГГГГ.ММ.ДД / ГГГГ-ММ-ДД и
	// ДД.ММ.ГГГГ / ДД-ММ-ГГГГ. Такие последовательности телефоном не считаются.
	dateRe = regexp.MustCompile(`(?:\d{4}[.\-]\d{1,2}[.\-]\d{1,2}|\d{1,2}[.\-]\d{1,2}[.\-]\d{4})`)
	// socialRe — URL соцсетей (LinkedIn/Twitter/Facebook/Instagram/Telegram).
	socialRe = regexp.MustCompile(`(?i)\bhttps?://(?:www\.)?(?:linkedin\.com|twitter\.com|x\.com|facebook\.com|instagram\.com|t\.me|telegram\.me)/[A-Za-z0-9_\-./?=&%#@+~]*`)
	// cfEmailRe — обфусцированный Cloudflare адрес в атрибуте data-cfemail.
	cfEmailRe = regexp.MustCompile(`(?i)data-cfemail=["']([0-9a-f]+)["']`)
)

// ExtractEmails извлекает адреса из текста, приводит к канонической форме и
// убирает дубли, сохраняя порядок первого появления.
func ExtractEmails(text string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, m := range emailRe.FindAllString(text, -1) {
		e := NormalizeEmail(m)
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}

// isPhoneCandidate проверяет, что найденный фрагмент текста похож на телефон, а
// не на дату или иную числовую последовательность. Условия: 7–15 цифр (нижняя
// граница — минимальная длина номера, верхняя — предел E.164); отсутствие
// узнаваемой формы даты; наличие телефонного разделителя (пробел, дефис,
// скобки) либо ведущего «+». Проверка идёт до нормализации, по исходному
// фрагменту, потому что разделители и «+» нормализация стирает.
func isPhoneCandidate(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	digits := NormalizePhone(s)
	if len(digits) < 7 || len(digits) > 15 {
		return false
	}
	if dateRe.MatchString(s) {
		return false
	}
	if !strings.HasPrefix(s, "+") && !strings.ContainsAny(s, " -()") {
		return false
	}
	return true
}

// ExtractPhones извлекает телефоны из текста (только цифры) и убирает дубли,
// сохраняя порядок первого появления. Кандидаты, не прошедшие проверку
// isPhoneCandidate (даты, слитные последовательности цифр без «+» и
// разделителей, строки вне диапазона 7–15 цифр), отбрасываются.
func ExtractPhones(text string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, m := range phoneRe.FindAllString(text, -1) {
		if !isPhoneCandidate(m) {
			continue
		}
		p := NormalizePhone(m)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// ExtractSocials извлекает URL соцсетей (LinkedIn/Twitter/Facebook/Instagram/
// Telegram) из текста и убирает дубли, сохраняя порядок первого появления.
func ExtractSocials(text string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, m := range socialRe.FindAllString(text, -1) {
		u := strings.TrimRight(m, ".,;:!?")
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// extractCloudflareEncoded возвращает hex-строки из data-cfemail в markdown.
func extractCloudflareEncoded(text string) []string {
	var out []string
	for _, sub := range cfEmailRe.FindAllStringSubmatch(text, -1) {
		out = append(out, sub[1])
	}
	return out
}
