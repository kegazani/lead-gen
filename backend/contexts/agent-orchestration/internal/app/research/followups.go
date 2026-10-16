package research

import (
	"net/url"
	"regexp"
	"strings"

	ra "lead-gen/contracts/integration-platform/researchacquisition"
)

// MarkdownLinks получает только фактически присутствующие ссылки источника.
// Неизвестные/сложные конструкции Markdown пропускаются, пути не угадываются.
var markdownLinks = regexp.MustCompile(`\[([^\]\n]{1,200})\]\(([^\s\)]+)(?:\s+"[^"\n]*")?\)`)

func SourceFollowups(receipt ra.Receipt) []Followup {
	out := []Followup{}
	seen := map[string]bool{}
	for _, artifact := range receipt.Artifacts {
		if artifact.Representation != "markdown" || artifact.SHA256 == "" {
			continue
		}
		base, err := url.Parse(artifact.URL)
		if err != nil || base.Hostname() == "" {
			continue
		}
		for _, match := range markdownLinks.FindAllStringSubmatchIndex(artifact.Content, 1000) {
			label := artifact.Content[match[2]:match[3]]
			raw := artifact.Content[match[4]:match[5]]
			parsed, err := url.Parse(raw)
			if err != nil || parsed.User != nil || strings.HasPrefix(raw, "#") {
				continue
			}
			resolved := base.ResolveReference(parsed)
			if resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Host, base.Host) {
				continue
			}
			normalized, ok := canonicalURL(resolved.String())
			if !ok || normalized == artifact.URL || seen[normalized] {
				continue
			}
			searchable := strings.ToLower(label + " " + resolved.Path)
			if decoded, err := url.PathUnescape(searchable); err == nil {
				searchable = decoded
			}
			reason := ""
			for _, word := range []string{"contact", "контакт", "kontakt", "обратная связь", "联系我们", "お問い合わせ"} {
				if strings.Contains(searchable, word) {
					reason = "source-contact-link"
					break
				}
			}
			if reason == "" {
				for _, word := range []string{"about", "о компании", "о нас", "team", "команда", "legal", "реквизит", "impressum", "company"} {
					if strings.Contains(searchable, word) {
						reason = "source-company-link"
						break
					}
				}
			}
			if reason == "" {
				continue
			}
			seen[normalized] = true
			out = append(out, Followup{URL: normalized, Reason: reason, SourceURL: artifact.URL, SourceRevision: artifact.SHA256, Representation: artifact.Representation, Start: match[0], End: match[1], Quote: artifact.Content[match[0]:match[1]]})
			if len(out) == 8 {
				return out
			}
		}
	}
	return out
}

func followupProven(receipt ra.Receipt, f Followup) bool {
	for _, artifact := range receipt.Artifacts {
		if artifact.URL != f.SourceURL || artifact.SHA256 != f.SourceRevision || artifact.Representation != f.Representation {
			continue
		}
		if f.Start < 0 || f.End <= f.Start || f.End > len(artifact.Content) || artifact.Content[f.Start:f.End] != f.Quote {
			return false
		}
		// Совпадение с проверенным разбором не позволяет Analyzer подставить иной URL
		// под существующий, но не относящийся к нему локатор.
		for _, proven := range SourceFollowups(ra.Receipt{Artifacts: []ra.Artifact{artifact}}) {
			if proven.URL == f.URL && proven.Start == f.Start && proven.End == f.End {
				return true
			}
		}
	}
	return false
}
