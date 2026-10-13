package app

import (
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/contracts/integration-platform/crawl"
)

// contactAttr — атрибут контакта с происхождением на этапе экстракции.
type contactAttr struct {
	value string
	prov  domain.AttributeProvenance
}

// contactCandidate — извлечённый контакт до построения доменного агрегата.
type contactCandidate struct {
	email contactAttr
	phone contactAttr
	role  contactAttr
}

// identity — идентификатор контакта: нормализованный email, иначе телефон.
func (c contactCandidate) identity() string {
	if e := NormalizeEmail(c.email.value); e != "" {
		return e
	}
	if p := NormalizePhone(c.phone.value); p != "" {
		return p
	}
	return ""
}

// hasContact сообщает, есть ли у контакта email или телефон (критерий качества).
func (c contactCandidate) hasContact() bool {
	return c.email.value != "" || c.phone.value != ""
}

// fillMissing дополняет отсутствующие поля из другого кандидата, не перетирая
// уже заполненные (приоритет дешёвых слоёв при слиянии с extract).
func (c *contactCandidate) fillMissing(other contactCandidate) {
	if c.email.value == "" && other.email.value != "" {
		c.email = other.email
	}
	if c.phone.value == "" && other.phone.value != "" {
		c.phone = other.phone
	}
	if c.role.value == "" && other.role.value != "" {
		c.role = other.role
	}
}

// toDomain собирает доменный агрегат контакта.
func (c contactCandidate) toDomain(companyID domain.CompanyID, at time.Time) (*domain.Contact, error) {
	var email, phone, role *domain.ContactAttribute
	if c.email.value != "" {
		email = &domain.ContactAttribute{Value: c.email.value, Provenance: c.email.prov}
	}
	if c.phone.value != "" {
		phone = &domain.ContactAttribute{Value: c.phone.value, Provenance: c.phone.prov}
	}
	if c.role.value != "" {
		role = &domain.ContactAttribute{Value: c.role.value, Provenance: c.role.prov}
	}
	return domain.CreateContact(domain.CreateContactParams{
		ID:        c.identity(),
		CompanyID: companyID,
		Email:     email,
		Phone:     phone,
		Role:      role,
		CreatedAt: at,
	})
}

// mergeContacts сливает кандидатов дешёвых слоёв и extract: значения дешёвых
// слоёв не перетираются, extract дополняет отсутствующие поля и добавляет новые
// контакты.
func mergeContacts(cheap, extracted []contactCandidate) []contactCandidate {
	byID := make(map[string]*contactCandidate)
	order := make([]string, 0, len(cheap)+len(extracted))

	for _, c := range cheap {
		id := c.identity()
		if id == "" {
			continue
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
			cc := c
			byID[id] = &cc
		}
	}
	for _, c := range extracted {
		id := c.identity()
		if id == "" {
			continue
		}
		if existing, ok := byID[id]; ok {
			existing.fillMissing(c)
		} else {
			order = append(order, id)
			cc := c
			byID[id] = &cc
		}
	}

	out := make([]contactCandidate, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// contactCandidatesFromExtract преобразует результат Firecrawl extract в
// кандидатов контактов с происхождением MethodLLM. Имя контакта в домене не
// хранится и отбрасывается.
func contactCandidatesFromExtract(candidates []crawl.ContactCandidate, page crawl.Page, at time.Time) []contactCandidate {
	prov := domain.AttributeProvenance{SourceURL: page.URL, ExtractedAt: at, Method: domain.MethodLLM}
	evidence := make(map[string]bool)
	phoneProof := make(map[string]string)
	for _, c := range extractContacts(page, at) {
		evidence[c.identity()] = true
		if c.phone.value != "" {
			phoneProof[c.phone.value] = c.phone.prov.Evidence
		}
	}
	// Обфускация допустима только когда она действительно присутствует на странице.
	decoded := dotWordRe.ReplaceAllString(atWordRe.ReplaceAllString(contactText(page.Markdown), "@"), ".")
	for _, email := range ExtractEmails(decoded) {
		evidence[email] = true
	}
	out := make([]contactCandidate, 0, len(candidates))
	for _, c := range candidates {
		email := NormalizeEmail(c.Email)
		if !validContactEmail(email) || !evidence[email] {
			email = ""
		}
		// Телефон от LLM проходит ту же проверку, что и regex-кандидаты: даты и
		// прочие числовые последовательности не должны попадать в контакты.
		phone := ""
		if evidence[NormalizePhone(c.Phone)] {
			phone = NormalizePhone(c.Phone)
		}
		if email == "" && phone == "" {
			// Ни email, ни телефона: у кандидата нет идентификатора, он бесполезен.
			continue
		}
		emailProv, phoneProv := prov, prov
		emailProv.Evidence = email
		phoneProv.Evidence = phoneProof[phone]
		out = append(out, contactCandidate{
			email: contactAttr{value: email, prov: emailProv},
			phone: contactAttr{value: phone, prov: phoneProv},
			role:  contactAttr{value: strings.TrimSpace(c.Role), prov: prov},
		})
	}
	return out
}

// extractLayer1 — слой 1: название из метаданных (title) либо markdown H1,
// описание из метаданных либо первого абзаца, соцсети из markdown.
func (r *RunSearch) extractLayer1(page crawl.Page) (name, description domain.AttributedValue, socials []domain.AttributedValue) {
	return extractCard(page, r.now())
}

// extractCard — слой 1 как свободная функция: собирает название, описание и
// соцсети страницы на указанный момент. Общий код планового обхода (RunSearch) и
// синхронного поиска под заявку (SearchLeadsForRequest).
func extractCard(page crawl.Page, at time.Time) (name, description domain.AttributedValue, socials []domain.AttributedValue) {
	prov := domain.Provenance{SourceURL: page.URL, ExtractedAt: at}

	// Приоритет у title: первый H1 в markdown часто — заголовок секции страницы,
	// а не название компании, тогда как title несёт имя сайта.
	nameValue := strings.TrimSpace(page.Metadata["og:site_name"])
	if nameValue == "" {
		nameValue = strings.TrimSpace(page.Metadata["title"])
	}
	if nameValue == "" {
		nameValue = h1FromMarkdown(page.Markdown)
	}
	nameValue = strings.TrimSpace(tagRe.ReplaceAllString(labelLinkRe.ReplaceAllString(nameValue, "$1"), ""))
	if usableCompanyName(nameValue) {
		name = domain.AttributedValue{Value: nameValue, Provenance: prov}
	}

	descValue := strings.TrimSpace(page.Metadata["description"])
	if descValue == "" {
		descValue = firstParagraph(page.Markdown)
	}
	if descValue != "" {
		description = domain.AttributedValue{Value: descValue, Provenance: prov}
	}

	for _, s := range ExtractSocials(page.Markdown) {
		socials = append(socials, domain.AttributedValue{Value: s, Provenance: prov})
	}
	return name, description, socials
}

// extractLayer2 — слой 2: email и телефон regex-нормализацией, деобфускация
// Cloudflare-адресов из markdown.
func (r *RunSearch) extractLayer2(page crawl.Page) []contactCandidate {
	return extractContacts(page, r.now())
}

// extractContacts — слой 2 как свободная функция: email и телефон
// regex-нормализацией, деобфускация Cloudflare-адресов из markdown. Общий код
// планового обхода (RunSearch) и синхронного поиска под заявку.
func extractContacts(page crawl.Page, at time.Time) []contactCandidate {
	seen := make(map[string]struct{})

	var contacts []contactCandidate

	add := func(id string, c contactCandidate) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		contacts = append(contacts, c)
	}

	for _, email := range ExtractEmails(contactText(page.Markdown)) {
		add(NormalizeEmail(email), contactCandidate{
			email: contactAttr{value: email, prov: domain.AttributeProvenance{SourceURL: page.URL, ExtractedAt: at, Method: domain.MethodRegex}},
		})
	}
	for _, phone := range extractPhoneEvidence(page.Markdown) {
		add(phone.number, contactCandidate{
			phone: contactAttr{value: phone.number, prov: domain.AttributeProvenance{SourceURL: page.URL, ExtractedAt: at, Method: domain.MethodRegex, Evidence: phone.evidence}},
		})
	}
	for _, encoded := range extractCloudflareEncoded(scriptRe.ReplaceAllString(codeRe.ReplaceAllString(page.Markdown, " "), " ")) {
		email, ok := DecodeCloudflareEmail(encoded)
		if !ok || !validContactEmail(email) {
			continue
		}
		add(NormalizeEmail(email), contactCandidate{
			email: contactAttr{value: email, prov: domain.AttributeProvenance{SourceURL: page.URL, ExtractedAt: at, Method: domain.MethodMarkdown}},
		})
	}
	return contacts
}

// h1FromMarkdown возвращает текст первого заголовка уровня 1 (#) в markdown.
func h1FromMarkdown(md string) string {
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(t, "# "))
		}
	}
	return ""
}

// firstParagraph возвращает первый содержательный абзац markdown (первая
// непустая строка, не заголовок и не разметка).
func firstParagraph(md string) string {
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") || strings.HasPrefix(t, ">") {
			continue
		}
		return t
	}
	return ""
}
