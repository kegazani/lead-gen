// Package researchmemory содержит правила свидетельств и scoped идентичности.
package researchmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	contract "lead-gen/contracts/lead-management/researchmemory"
)

const MaxContentBytes = 2 << 20

func ValidateScope(s contract.Scope) error {
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.Purpose) == "" || len(s.TenantID) > 200 || len(s.Purpose) > 200 {
		return fmt.Errorf("%w: требуется tenant и purpose", contract.ErrInvalid)
	}
	return nil
}
func Hash(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func ContentHash(content string) string {
	s := sha256.Sum256([]byte(content))
	return hex.EncodeToString(s[:])
}
func StableID(prefix string, values ...string) string { return prefix + Hash(values)[:32] }
func ValidLocator(content string, l contract.Locator) bool {
	return l.Start >= 0 && l.End > l.Start && l.End <= len(content) && l.Quote != "" && content[l.Start:l.End] == l.Quote
}
func NormalizeObservation(r contract.SaveObservationRequest) (contract.SaveObservationRequest, error) {
	r.Claims = append([]contract.Claim(nil), r.Claims...)
	r.Identifiers = append([]contract.Identifier(nil), r.Identifiers...)
	fail := func(reason string) (contract.SaveObservationRequest, error) {
		return r, fmt.Errorf("%w: %s", contract.ErrInvalid, reason)
	}
	if err := ValidateScope(r.Scope); err != nil {
		return r, err
	}
	if r.ObservationID == "" || r.MissionID == "" || r.ActionID == "" || r.QueryAttemptID == "" || len(r.ObservationID) > 200 || len(r.MissionID) > 200 || len(r.ActionID) > 200 || len(r.QueryAttemptID) > 200 {
		return fail("неполное происхождение наблюдения")
	}
	if len(r.Source.Content) == 0 || len(r.Source.Content) > MaxContentBytes || len(r.Source.URL) > 2048 || len(r.Source.Revision) > 200 || len(r.Source.Family) > 500 || len(r.Source.Authority) > 200 {
		return fail("размер снимка источника")
	}
	u, e := url.Parse(r.Source.URL)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return fail("URL источника")
	}
	if r.Source.Revision == "" || r.Source.ObservedAt.IsZero() || r.Source.ObservedAt.After(time.Now().Add(5*time.Minute)) {
		return fail("версия и дата источника")
	}
	switch r.Source.Representation {
	case "markdown", "text", "html", "ocr-text":
	default:
		return fail("представление источника")
	}
	switch r.Source.Kind {
	case "page", "registry", "search-snippet":
	default:
		return fail("тип источника")
	}
	if r.Source.Kind == "registry" && r.Source.Authority == "" {
		return fail("реестр без органа")
	}
	if r.Source.Family == "" {
		r.Source.Family = "unknown:" + strings.ToLower(u.Hostname())
	}
	r.Subject.Name = strings.TrimSpace(r.Subject.Name)
	if r.Subject.Name == "" || r.Subject.Key == "" || len(r.Subject.Name) > 2000 || len(r.Subject.Key) > 500 || !ValidLocator(r.Source.Content, r.Subject.Locator) || !strings.Contains(r.Subject.Locator.Quote, r.Subject.Name) {
		return fail("субъект должен присутствовать в сохранённом фрагменте")
	}
	switch r.Subject.Kind {
	case "company", "legal-entity", "branch", "brand", "discovery-hit":
	default:
		return fail("тип субъекта")
	}
	r.Subject.Country = strings.ToUpper(strings.TrimSpace(r.Subject.Country))
	if len(r.Subject.Country) != 0 && len(r.Subject.Country) != 2 {
		return fail("страна субъекта")
	}
	if len(r.Claims) > 100 || len(r.Identifiers) > 20 {
		return fail("слишком много утверждений")
	}
	for i := range r.Identifiers {
		id := &r.Identifiers[i]
		id.Jurisdiction = strings.ToUpper(strings.TrimSpace(id.Jurisdiction))
		id.Authority = strings.TrimSpace(id.Authority)
		id.Kind = strings.ToLower(strings.TrimSpace(id.Kind))
		if !ValidLocator(r.Source.Content, id.Locator) || !within(id.Locator, r.Subject.Locator) || !strings.Contains(id.Locator.Quote, id.Original) {
			return fail("идентификатор вне блока субъекта")
		}
		if id.Method == "" || id.MethodVersion == "" {
			return fail("не задан метод идентификатора")
		}
		id.Normalized, id.Format = NormalizeIdentifier(*id)
		id.Existence = "unknown"
		if id.Format == "valid" && r.Source.Kind == "registry" && id.Authority == r.Source.Authority && id.Method == "registry-record" {
			id.Existence = "confirmed"
		}
	}
	for i := range r.Claims {
		c := &r.Claims[i]
		if c.Predicate == "" || c.Value == "" || c.Method == "" || c.MethodVersion == "" || !ValidLocator(r.Source.Content, c.Locator) {
			return fail("утверждение без проверяемого фрагмента или метода")
		}
		if c.Original == "" {
			c.Original = c.Value
		}
		if !strings.Contains(c.Locator.Quote, c.Original) {
			return fail("значение отсутствует в свидетельстве")
		}
		if c.SubjectKey != r.Subject.Key || !within(c.Locator, r.Subject.Locator) {
			return fail("утверждение относится к другому субъекту")
		}
		switch c.Knowledge {
		case "", "observed":
			c.Knowledge = "observed"
		case "hypothesis", "refuted", "conflicted":
		case "confirmed":
			if c.Method != "subject-block" || r.Source.Kind == "search-snippet" || r.Subject.Kind == "discovery-hit" {
				return fail("подтверждение требует предметного блока источника")
			}
		default:
			return fail("состояние знания")
		}
		if c.ValidFrom != nil && c.ValidUntil != nil && !c.ValidUntil.After(*c.ValidFrom) {
			return fail("интервал применимости")
		}
		if c.FreshUntil != nil && !c.FreshUntil.After(r.Source.ObservedAt) {
			return fail("срок актуальности")
		}
		switch c.Predicate {
		case "email":
			normalized, ok := NormalizeEmail(c.Value)
			if !ok {
				return fail("некорректный email")
			}
			c.Value = normalized
		case "phone":
			normalized, ok := NormalizePhone(c.Value)
			if !ok {
				return fail("телефон требует однозначной международной записи")
			}
			c.Value = normalized
		}
	}
	r.Source.ContentHash = ContentHash(r.Source.Content)
	r.Source.ID = StableID("src_", r.Scope.TenantID, r.Scope.Purpose, r.Source.URL, r.Source.Revision, ContentHash(r.Source.Content))
	return r, nil
}
func within(a, b contract.Locator) bool { return a.Start >= b.Start && a.End <= b.End }
func NormalizeEmail(v string) (string, bool) {
	if strings.ContainsAny(v, "\r\n") {
		return "", false
	}
	a, e := mail.ParseAddress(v)
	if e != nil || a.Address != v {
		return "", false
	}
	i := strings.LastIndex(v, "@")
	if i < 1 || i > 64 || len(v) > 254 || !strings.Contains(v[i+1:], ".") {
		return "", false
	}
	return v[:i+1] + strings.ToLower(v[i+1:]), true
}

// Неоднозначный местный номер не превращается в международный по IP или языку.
func NormalizePhone(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "+") {
		return "", false
	}
	var b strings.Builder
	b.WriteByte('+')
	for _, c := range v[1:] {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else if c != ' ' && c != '-' && c != '(' && c != ')' {
			return "", false
		}
	}
	n := b.String()
	if len(n) < 9 || len(n) > 16 || n[1] == '0' {
		return "", false
	}
	return n, true
}

var gbID = regexp.MustCompile(`^(?:[0-9]{8}|[A-Z]{2}[0-9]{6})$`)

// NormalizeIdentifier проверяет лишь формат, не существование юрлица.
func NormalizeIdentifier(id contract.Identifier) (string, string) {
	v := strings.ToUpper(strings.TrimSpace(id.Original))
	if v == "" || strings.Trim(v, "0") == "" {
		return v, "invalid"
	}
	j := strings.ToUpper(id.Jurisdiction)
	if strings.IndexFunc(v, unicode.IsSpace) >= 0 {
		return v, "invalid"
	}
	digits := func(s string) bool {
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	switch j + ":" + id.Kind {
	case "RU:inn":
		if !digits(v) || (len(v) != 10 && len(v) != 12) {
			return v, "invalid"
		}
		check := func(weights []int, last int) bool {
			sum := 0
			for i, w := range weights {
				sum += int(v[i]-'0') * w
			}
			return sum%11%10 == int(v[last]-'0')
		}
		if len(v) == 10 && check([]int{2, 4, 10, 3, 5, 9, 4, 6, 8}, 9) {
			return v, "valid"
		}
		if len(v) == 12 && check([]int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}, 10) && check([]int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}, 11) {
			return v, "valid"
		}
		return v, "invalid"
	case "GB:company-number":
		if gbID.MatchString(v) {
			if digits(v) {
				return v, "valid"
			}
			switch v[:2] {
			case "SC", "NI", "OC", "SO", "FC", "BR", "LP", "SL", "NC", "NF", "NL", "OE", "RC", "IC", "SE":
				return v, "valid"
			default:
				return v, "unknown"
			}
		}
		return v, "invalid"
	case "FR:siren", "FR:siret":
		n := 9
		if id.Kind == "siret" {
			n = 14
		}
		if len(v) != n || !digits(v) {
			return v, "invalid"
		}
		sum := 0
		for i, c := range v {
			x := int(c - '0')
			if (n-i)%2 == 0 {
				x *= 2
				if x > 9 {
					x -= 9
				}
			}
			sum += x
		}
		if sum%10 == 0 {
			return v, "valid"
		}
		return v, "invalid"
	default:
		return v, "unknown"
	}
}
func StrongIdentifier(id contract.Identifier) bool {
	return id.Existence == "confirmed" && id.Format == "valid" && id.Authority != "" && id.Jurisdiction != "" && (id.Kind == "inn" || id.Kind == "company-number" || id.Kind == "siren")
}
