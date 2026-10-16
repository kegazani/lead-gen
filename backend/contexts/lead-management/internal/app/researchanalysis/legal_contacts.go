package researchanalysis

import (
	"regexp"
	"strings"
	"unicode/utf8"

	rules "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	model "lead-gen/contracts/integration-platform/llm"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

var legalRUIdentifier = regexp.MustCompile(`(?i)^ИНН\s*[:№]?\s*([0-9]{10,12})(?:\s*КПП\s*[:№]?\s*[0-9]{9})?$`)
var legalGBIdentifier = regexp.MustCompile(`(?i)^(?:company(?: registration)? number|registered number|company no\.?)\s*[:#]?\s*([a-z0-9]{8})$`)
var legalFRIdentifier = regexp.MustCompile(`(?i)^(SIREN|SIRET)\s*:?\s*([0-9]{9,14})$`)
var legalPhoneField = regexp.MustCompile(`(?i)^(?:телефоны?|тел\.?|phone|telephone|téléphone)\s*:\s*([+0-9()\s,./-]+)$`)
var legalEmailField = regexp.MustCompile(`(?i)^(?:e-?mail|электронная почта|почта|courriel)\s*:\s*(.+)$`)
var legalEmailValue = regexp.MustCompile(`^[^\s<>()\[\]@]+@[^\s<>()\[\]@]+$`)
var legalMailtoValue = regexp.MustCompile(`^\[([^\s\[\]]+)\]\(mailto:([^\s()]+)\)$`)

// legalDetailsContactEvidence допускает пустые строки только в самостоятельном
// коротком разделе реквизитов. Это локальная принадлежность контакта, не
// подтверждение существования юрлица или авторитетности сайта.
func legalDetailsContactEvidence(subject model.ResearchSubject, contact model.ResearchContact, subjects []model.ResearchSubject) bool {
	if utf8.RuneCountInString(subject.Block) > 1000 || containsOtherSubject(subject.Block, subject.Name, subjects) {
		return false
	}
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(subject.Block, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) < 4 || len(lines) > 12 || !legalDetailsHeading(lines[0]) || legalPlainLine(lines[1]) != subject.Name {
		return false
	}
	identifierFound, contactFound := false, false
	for _, raw := range lines[2:] {
		line := legalPlainLine(raw)
		if id, ok := legalIdentifierLine(line); ok {
			if identifierFound {
				return false
			}
			_, format := rules.NormalizeIdentifier(id)
			if format != "valid" || !quotedSubjectIdentifier(subject, id) || !strings.Contains(contact.Quote, id.Original) {
				return false
			}
			identifierFound = true
			continue
		}
		if match := legalPhoneField.FindStringSubmatch(line); match != nil {
			if contact.Kind == "phone" && strings.Contains(match[1], contact.Value) {
				contactFound = true
			}
			continue
		}
		if match := legalEmailField.FindStringSubmatch(line); match != nil {
			value := match[1]
			if link := legalMailtoValue.FindStringSubmatch(value); link != nil {
				if link[1] != link[2] {
					return false
				}
				value = link[1]
			}
			if !legalEmailValue.MatchString(value) {
				return false
			}
			if contact.Kind == "email" && value == contact.Value {
				contactFound = true
			}
			continue
		}
		if legalAddressLine(line) {
			continue
		}
		// Следующий heading, второе имя, narrative и нераспознанное поле
		// прекращают доказательство; нельзя захватить соседнюю карточку/footer.
		return false
	}
	return identifierFound && contactFound
}

func legalPlainLine(line string) string {
	line = strings.TrimSpace(line)
	for _, wrapper := range []string{"**", "__"} {
		if strings.HasPrefix(line, wrapper) && strings.HasSuffix(line, wrapper) && len(line) > len(wrapper)*2 {
			return strings.TrimSpace(line[len(wrapper) : len(line)-len(wrapper)])
		}
	}
	return line
}

func legalDetailsHeading(raw string) bool {
	line := strings.TrimSpace(strings.TrimLeft(legalPlainLine(raw), "#"))
	line = strings.ToLower(strings.TrimSuffix(line, ":"))
	switch line {
	case "реквизиты", "наши реквизиты", "реквизиты компании", "реквизиты организации", "company details", "legal details", "registered company details", "mentions légales", "informations légales":
		return true
	}
	return false
}

func legalIdentifierLine(line string) (memory.Identifier, bool) {
	if match := legalRUIdentifier.FindStringSubmatch(line); match != nil {
		return memory.Identifier{Kind: "inn", Jurisdiction: "RU", Original: match[1]}, true
	}
	if match := legalGBIdentifier.FindStringSubmatch(line); match != nil {
		return memory.Identifier{Kind: "company-number", Jurisdiction: "GB", Original: match[1]}, true
	}
	if match := legalFRIdentifier.FindStringSubmatch(line); match != nil {
		return memory.Identifier{Kind: strings.ToLower(match[1]), Jurisdiction: "FR", Original: match[2]}, true
	}
	return memory.Identifier{}, false
}

func quotedSubjectIdentifier(subject model.ResearchSubject, expected memory.Identifier) bool {
	for _, id := range subject.Identifiers {
		if id.Kind == expected.Kind && id.Jurisdiction == expected.Jurisdiction && id.Value == expected.Original && strings.Contains(id.Quote, expected.Original) {
			if _, ok := locate(subject.Block, id.Quote, 0); ok {
				return true
			}
		}
	}
	return false
}

func legalAddressLine(line string) bool {
	if utf8.RuneCountInString(line) > 250 || strings.ContainsAny(line, "@<>[]") {
		return false
	}
	line = strings.ToLower(line)
	for _, prefix := range []string{"адрес:", "юридический адрес:", "фактический адрес:", "россия,", "российская федерация,", "address:", "registered office:", "adresse:", "siège social:"} {
		if strings.HasPrefix(line, prefix) && strings.TrimSpace(line[len(prefix):]) != "" {
			return true
		}
	}
	return false
}
