package researchmemory

import (
	"net/url"
	"regexp"
	"strings"

	contract "lead-gen/contracts/lead-management/researchmemory"
)

var markdownNavigation = regexp.MustCompile(`\[[^\]\r\n]{0,200}\]\(<?([^\s)>]+)>?(?:\s+"[^"\r\n]*")?\)`)
var htmlNavigation = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)

func IdentityName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
func SameOrigin(left, right string) bool {
	a, e := url.Parse(left)
	if e != nil {
		return false
	}
	b, e := url.Parse(right)
	return e == nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// NavigationCandidate возвращает только точный сохранённый переход. Это сигнал
// review; ссылка и одинаковое имя не подтверждают юридическое тождество.
func NavigationCandidate(source contract.Source, targetURL string) (contract.Locator, bool) {
	if !SameOrigin(source.URL, targetURL) {
		return contract.Locator{}, false
	}
	base, e := url.Parse(source.URL)
	if e != nil {
		return contract.Locator{}, false
	}
	target, e := url.Parse(targetURL)
	if e != nil {
		return contract.Locator{}, false
	}
	target.Fragment = ""
	if target.Path == "" {
		target.Path = "/"
	}
	patterns := []*regexp.Regexp{markdownNavigation}
	if source.Representation == "html" {
		patterns = append(patterns, htmlNavigation)
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatchIndex(source.Content, 256) {
			if len(match) < 4 {
				continue
			}
			raw := source.Content[match[2]:match[3]]
			link, e := url.Parse(raw)
			if e != nil {
				continue
			}
			resolved := base.ResolveReference(link)
			resolved.Fragment = ""
			if resolved.Path == "" {
				resolved.Path = "/"
			}
			if resolved.String() != target.String() {
				continue
			}
			return contract.Locator{Start: match[0], End: match[1], Quote: source.Content[match[0]:match[1]]}, true
		}
	}
	return contract.Locator{}, false
}
