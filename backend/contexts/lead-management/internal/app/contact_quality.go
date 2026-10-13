package app

import (
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/contracts/integration-platform/crawl"
)

var (
	atWordRe      = regexp.MustCompile(`(?i)\s*(?:\[at\]|\(at\))\s*`)
	dotWordRe     = regexp.MustCompile(`(?i)\s*(?:\[dot\]|\(dot\))\s*`)
	phoneLabelRe  = regexp.MustCompile(`(?i)(?:\b(?:tel|telephone|phone|mobile|call|fax)\b|тел(?:ефон)?|звон|мобильн|факс)`)
	numberLabelRe = regexp.MustCompile(`(?i)(?:\b(?:id|order|price|article|isbn|sku|views)\b|инн|огрн|цена|стоимость|просмотр)`)
	webURLRe      = regexp.MustCompile(`(?i)https?://[^\s<>"')]+`)
	telLinkRe     = regexp.MustCompile(`(?i)\btel:((?:%[0-9a-f]{2}|[+0-9(). -])+)`)
	codeRe        = regexp.MustCompile("(?s)```.*?```|`[^`]*`")
	scriptRe      = regexp.MustCompile(`(?is)<(?:script|style)\b[^>]*>.*?</(?:script|style)>`)
	tagRe         = regexp.MustCompile(`<[^>]*>`)
	imageRe       = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	labelLinkRe   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)

// validContactEmail отсекает не адреса, а имена файлов вроде logo@2x.png.
// Синтаксическая проверка не является проверкой доставки.
func validContactEmail(raw string) bool {
	e := NormalizeEmail(raw)
	parsed, err := mail.ParseAddress(e)
	if err != nil || parsed.Address != e || emailRe.FindString(e) != e {
		return false
	}
	parts := strings.Split(e, "@")
	if len(parts) != 2 || strings.HasPrefix(parts[0], ".") || strings.HasSuffix(parts[0], ".") || strings.Contains(e, "..") {
		return false
	}
	for _, label := range strings.Split(parts[1], ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	switch parts[1][strings.LastIndex(parts[1], ".")+1:] {
	case "png", "jpg", "jpeg", "gif", "webp", "svg", "css", "js", "woff", "woff2":
		return false
	}
	return true
}

// companySource запрещает превращать публикацию в компанию её издателя.
func companySource(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return false
	}
	for _, segment := range strings.Split(strings.ToLower(u.Path), "/") {
		switch segment {
		case "article", "articles", "blog", "blogs", "news", "post", "posts":
			return false
		}
	}
	return true
}

func usableCompanyName(name string) bool {
	if !strings.ContainsFunc(name, unicode.IsLetter) {
		return false
	}
	lower := strings.ToLower(name)
	for _, marker := range []string{"access denied", "page not found", "just a moment", "captcha", "доступ запрещён", "страница не найдена"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// contactText убирает код, URL и разметку до поиска числовых последовательностей.
func contactText(text string) string {
	text = codeRe.ReplaceAllString(text, " ")
	text = scriptRe.ReplaceAllString(text, " ")
	text = imageRe.ReplaceAllString(text, " ")
	text = webURLRe.ReplaceAllString(text, " ")
	return tagRe.ReplaceAllString(text, " ")
}

func plausiblePhoneDigits(digits string) bool {
	if len(digits) < 10 || len(digits) > 15 {
		return false
	}
	return strings.Trim(digits, digits[:1]) != ""
}

// usableStoredContact не удаляет старые данные. Подозрительные значения остаются
// в карточке отдельно и не служат доказательством пригодности компании в пуле.
func usableStoredContact(c *domain.Contact) bool {
	return storedContactIssue(c) == ""
}

func storedContactIssue(c *domain.Contact) string {
	found := false
	if e := c.Email(); e != nil && e.Value != "" {
		if !validContactEmail(e.Value) || !companySource(e.Provenance.SourceURL) {
			return "email_or_source"
		}
		if e.Provenance.Method == domain.MethodLLM && e.Provenance.Evidence == "" {
			return "email_unconfirmed"
		}
		found = true
	}
	if p := c.Phone(); p != nil && p.Value != "" {
		if !companySource(p.Provenance.SourceURL) || !plausiblePhoneDigits(NormalizePhone(p.Value)) {
			return "phone_or_source"
		}
		confirmed := false
		for _, number := range ExtractPhones(p.Provenance.Evidence) {
			if number == NormalizePhone(p.Value) {
				confirmed = true
				break
			}
		}
		if !confirmed {
			return "phone_unconfirmed"
		}
		found = true
	}
	if !found {
		return "missing"
	}
	return ""
}

func usableCrawlPage(page crawl.Page, name domain.AttributedValue, contacts []contactCandidate) bool {
	if !companySource(page.URL) || !usableCompanyName(name.Value) || NormalizeDomain(page.URL) == "" {
		return false
	}
	for _, c := range contacts {
		if c.hasContact() {
			return true
		}
	}
	return false
}
