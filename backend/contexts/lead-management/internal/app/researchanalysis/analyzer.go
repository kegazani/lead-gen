// Package researchanalysis превращает предложения модели в наблюдения с
// проверенными ссылками на неизменённый снимок источника.
package researchanalysis

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	rules "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	model "lead-gen/contracts/integration-platform/llm"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

const MethodVersion = "subject-evidence/v2"

type Config struct {
	Model            model.ResearchExtraction
	ModelID          string
	ContactFreshness time.Duration
	MaxSubjects      int
	ModelTimeout     time.Duration
}

type Analyzer struct{ config Config }

func New(config Config) (*Analyzer, error) {
	if config.ModelTimeout == 0 {
		config.ModelTimeout = 120 * time.Second
	}
	if config.Model == nil || config.ModelID == "" || config.ContactFreshness <= 0 || config.ContactFreshness > 90*24*time.Hour || config.MaxSubjects < 1 || config.MaxSubjects > 32 {
		return nil, fmt.Errorf("researchanalysis: требуются модель, её версия и явные технические пределы")
	}
	if config.ModelTimeout < time.Millisecond || config.ModelTimeout > 2*time.Minute {
		return nil, fmt.Errorf("researchanalysis: некорректный timeout модели")
	}
	return &Analyzer{config: config}, nil
}

func (a *Analyzer) Analyze(ctx context.Context, request memory.AnalysisRequest) (memory.AnalysisResult, error) {
	result := memory.AnalysisResult{ModelID: a.config.ModelID, MethodVersion: MethodVersion}
	if rules.ValidateScope(request.Scope) != nil || request.MissionID == "" || request.ActionID == "" || request.QueryAttemptID == "" || request.CriteriaVersion < 1 || strings.TrimSpace(request.ICP) == "" || request.Source.Revision == "" || request.Source.ObservedAt.IsZero() {
		return result, fmt.Errorf("%w: неполное происхождение анализа", memory.ErrInvalid)
	}
	if request.Source.ObservedAt.After(time.Now().Add(5*time.Minute)) || (request.Source.Kind != "page" && request.Source.Kind != "registry" && request.Source.Kind != "search-snippet") || (request.Source.Kind == "registry" && request.Source.Authority == "") {
		return result, fmt.Errorf("%w: некорректные метаданные источника", memory.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	criteria := make([]model.ResearchCriterion, 0, len(request.Criteria)+2)
	for _, c := range request.Criteria {
		if strings.HasPrefix(c.ID, "__human_") {
			return result, fmt.Errorf("%w: зарезервированный ID критерия", memory.ErrInvalid)
		}
		criteria = append(criteria, model.ResearchCriterion{ID: c.ID, Text: c.Text, Kind: c.Kind})
	}
	// ICP задан человеком и остаётся обязательным условием даже при отсутствии
	// отдельно введённых must. Страна не подставляется в субъект из запроса.
	criteria = append(criteria, model.ResearchCriterion{ID: "__human_icp", Text: request.ICP, Kind: "must"})
	if request.Country != "" {
		criteria = append(criteria, model.ResearchCriterion{ID: "__human_market", Text: "Компания присутствует или работает на выбранном рынке " + request.Country, Kind: "must"})
	}
	modelRequest := model.ResearchExtractRequest{URL: request.Source.URL, Representation: request.Source.Representation, Content: request.Source.Content, ICP: request.ICP, Country: request.Country, Criteria: criteria, MaxSubjects: a.config.MaxSubjects}
	if err := modelRequest.Validate(); err != nil {
		return result, fmt.Errorf("%w: некорректное задание извлечения", memory.ErrInvalid)
	}
	callCtx, cancel := context.WithTimeout(ctx, a.config.ModelTimeout)
	extracted, _, err := a.config.Model.ExtractResearch(callCtx, modelRequest)
	cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err != nil {
		return result, fmt.Errorf("researchanalysis: извлечение недоступно: %w", err)
	}
	if err := extracted.ValidateBounds(a.config.MaxSubjects); err != nil {
		return result, fmt.Errorf("researchanalysis: нарушена схема или технические пределы извлечения")
	}
	if len(extracted.Subjects) == a.config.MaxSubjects {
		result.Diagnostics = append(result.Diagnostics, "subject-limit-reached")
	}
	seen := map[string]bool{}
	for _, subject := range extracted.Subjects {
		name := strings.TrimSpace(subject.Name)
		block, ok := locate(request.Source.Content, subject.Block, 0)
		if !ok || utf8.RuneCountInString(subject.Block) > 6000 || utf8.RuneCountInString(name) > 500 || !strings.Contains(subject.Block, name) || !strings.ContainsFunc(name, unicode.IsLetter) {
			result.Diagnostics = append(result.Diagnostics, "subject-evidence-invalid")
			continue
		}
		switch subject.Kind {
		case "company", "legal-entity", "branch", "brand":
		default:
			result.Diagnostics = append(result.Diagnostics, "subject-kind-invalid")
			continue
		}
		key := rules.StableID("subject-", strings.ToLower(strings.Join(strings.Fields(name), " ")), subject.Kind)
		if seen[key] {
			result.Diagnostics = append(result.Diagnostics, "subject-duplicate")
			continue
		}
		seen[key] = true
		observation := memory.SaveObservationRequest{Scope: request.Scope, MissionID: request.MissionID, ActionID: request.ActionID, QueryAttemptID: request.QueryAttemptID, Source: request.Source, Subject: memory.Subject{Key: key, Name: name, Kind: subject.Kind, Locator: block}}
		observation.ObservationID = rules.StableID("observation-", request.MissionID, request.ActionID, request.Source.URL, request.Source.Revision, key)
		countryExplicit := false
		country := strings.ToUpper(subject.Country)
		if len(country) == 2 {
			if location, ok := locate(subject.Block, subject.CountryQuote, block.Start); ok && ownsQuote(subject.CountryQuote, subject.Block, name, extracted.Subjects, false) {
				countryExplicit = countryMention(country, subject.CountryQuote)
				knowledge := "hypothesis"
				if countryExplicit {
					observation.Subject.Country = country
					knowledge = "observed"
				}
				observation.Claims = append(observation.Claims, memory.Claim{Predicate: "operating-market", Value: country, Original: subject.CountryQuote, SubjectKey: key, Locator: location, Knowledge: knowledge, Method: "llm-country-normalization", MethodVersion: a.evidenceMethodVersion()})
				if !countryExplicit {
					result.Diagnostics = append(result.Diagnostics, "country-normalization-inferred")
				}
			} else {
				result.Diagnostics = append(result.Diagnostics, "country-evidence-unproven")
			}
		}
		freshUntil := request.Source.ObservedAt.Add(a.config.ContactFreshness)
		for _, contact := range subject.Contacts {
			if contact.Kind != "email" && contact.Kind != "phone" {
				result.Diagnostics = append(result.Diagnostics, "contact-kind-invalid")
				continue
			}
			locator, ok := locate(subject.Block, contact.Quote, block.Start)
			legalSection := request.Source.Kind != "search-snippet" && legalDetailsContactEvidence(subject, contact, extracted.Subjects)
			if !ok || utf8.RuneCountInString(contact.Quote) > 1000 || contact.Value == "" || !strings.Contains(contact.Quote, contact.Value) || !ownsQuote(contact.Quote, subject.Block, name, extracted.Subjects, true) || (!localContactEvidence(contact.Quote, name, contact.Value) && !legalSection) {
				result.Diagnostics = append(result.Diagnostics, "contact-ownership-unproven")
				continue
			}
			claim := memory.Claim{Predicate: contact.Kind, Value: contact.Value, Original: contact.Value, SubjectKey: key, Locator: locator, Knowledge: "confirmed", Method: "subject-block", MethodVersion: a.evidenceMethodVersion(), FreshUntil: &freshUntil}
			if legalSection {
				// Полный локатор включает heading, имя, ID и контактные поля,
				// поэтому основание нового правила можно проверить по source.
				claim.Locator = block
				claim.MethodVersion += "/legal-details/v1"
			}
			if request.Source.Kind == "search-snippet" {
				claim.Knowledge = "observed"
			}
			// Одно плохое число не уничтожает полезную компанию. Те же правила
			// владельца проверяют формат/назначение до включения контакта.
			probe := observation
			probe.Claims = []memory.Claim{claim}
			if _, err := rules.NormalizeObservation(probe); err != nil {
				result.Diagnostics = append(result.Diagnostics, "contact-format-invalid")
				continue
			}
			observation.Claims = append(observation.Claims, claim)
		}
		for _, identifier := range subject.Identifiers {
			locator, ok := locate(subject.Block, identifier.Quote, block.Start)
			if !ok || identifier.Value == "" || !strings.Contains(identifier.Quote, identifier.Value) || !ownsQuote(identifier.Quote, subject.Block, name, extracted.Subjects, false) || len(observation.Identifiers) >= 20 {
				result.Diagnostics = append(result.Diagnostics, "identifier-evidence-unproven")
				continue
			}
			// Модель не присваивает registry-record и не подтверждает существование
			// идентификатора. Это отдельный реестровый адаптер и проверка владельца.
			id := memory.Identifier{Kind: identifier.Kind, Jurisdiction: identifier.Jurisdiction, Authority: identifier.Authority, Original: identifier.Value, Method: "llm-observation", MethodVersion: a.evidenceMethodVersion(), Locator: locator}
			probe := observation
			probe.Claims = nil
			probe.Identifiers = []memory.Identifier{id}
			if _, err := rules.NormalizeObservation(probe); err != nil {
				result.Diagnostics = append(result.Diagnostics, "identifier-evidence-unproven")
				continue
			}
			observation.Identifiers = append(observation.Identifiers, id)
		}
		accepted, reason, claims, evidence := assess(criteria, subject.Criteria, observation, extracted.Subjects, request.CriteriaVersion)
		for i := range claims {
			claims[i].MethodVersion = a.evidenceMethodVersion()
		}
		observation.Claims = append(observation.Claims, claims...)
		if request.Country != "" && (!countryExplicit || country != request.Country) {
			accepted = false
			reason = "market-evidence-unknown"
			if countryExplicit && country != request.Country {
				reason = "market-evidence-mismatch"
			}
		}
		if request.Source.Kind == "search-snippet" {
			accepted = false
			reason = "discovery-evidence-only"
		}
		normalized, err := rules.NormalizeObservation(observation)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, "observation-invalid")
			continue
		}
		result.Observations = append(result.Observations, memory.AnalyzedObservation{Observation: normalized, Accepted: accepted, Reason: reason, EvidenceRefs: evidence})
	}
	return result, nil
}

func (a *Analyzer) evidenceMethodVersion() string {
	return MethodVersion + "/" + model.ResearchExtractionSchemaVersion + "/" + a.config.ModelID
}

func locate(content, quote string, offset int) (memory.Locator, bool) {
	if quote == "" || !utf8.ValidString(content) || !utf8.ValidString(quote) {
		return memory.Locator{}, false
	}
	i := strings.Index(content, quote)
	if i < 0 || strings.Index(content[i+len(quote):], quote) >= 0 {
		return memory.Locator{}, false
	}
	return memory.Locator{Start: offset + i, End: offset + i + len(quote), Quote: quote}, true
}

func containsOtherSubject(quote, name string, subjects []model.ResearchSubject) bool {
	for _, s := range subjects {
		if s.Name != "" && s.Name != name && strings.Contains(quote, s.Name) {
			return true
		}
	}
	return false
}

func ownsQuote(quote, block, name string, subjects []model.ResearchSubject, requireName bool) bool {
	if containsOtherSubject(quote, name, subjects) {
		return false
	}
	return (!requireName && !containsOtherSubject(block, name, subjects)) || strings.Contains(quote, name)
}

// Контакт в соседнем footer/разделе каталога не подтверждает связь. Если имя и
// контакт не помещаются в один короткий абзац, сохраняется компания без контакта.
func localContactEvidence(quote, name, value string) bool {
	n, v := strings.Index(quote, name), strings.Index(quote, value)
	if n < 0 || v < 0 {
		return false
	}
	start, end := min(n, v), max(n+len(name), v+len(value))
	span := strings.ReplaceAll(quote[start:end], "\r\n", "\n")
	if utf8.RuneCountInString(span) > 350 {
		return false
	}
	for _, marker := range []string{"\n\n", "\n#", "\n---", "<footer"} {
		if strings.Contains(strings.ToLower(span), marker) {
			return false
		}
	}
	return true
}

func countryMention(code, quote string) bool {
	code = strings.ToUpper(code)
	for _, token := range strings.FieldsFunc(quote, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if token == code {
			return true
		}
	}
	// Консервативный словарь явных названий; язык страницы и рынок запроса
	// сами по себе не доказывают географию субъекта.
	aliases := map[string][]string{"RU": {"россия", "россии", "russia", "russian federation"}, "GB": {"united kingdom", "great britain", "великобритания", "великобритании"}, "US": {"united states", "usa", "сша"}, "DE": {"germany", "deutschland", "германия", "германии"}, "FR": {"france", "франция", "франции"}, "AE": {"united arab emirates", "оаэ"}}
	words := " " + strings.Join(strings.FieldsFunc(strings.ToLower(quote), func(r rune) bool { return !unicode.IsLetter(r) }), " ") + " "
	for _, name := range aliases[code] {
		if strings.Contains(words, " "+name+" ") {
			return true
		}
	}
	return false
}

func assess(criteria []model.ResearchCriterion, findings []model.ResearchCriterionFinding, observation memory.SaveObservationRequest, subjects []model.ResearchSubject, criteriaVersion int) (bool, string, []memory.Claim, []string) {
	known := map[string]model.ResearchCriterion{}
	for _, c := range criteria {
		known[c.ID] = c
	}
	byID := map[string]model.ResearchCriterionFinding{}
	accepted, reason := true, "criteria-evidence-accepted"
	for _, finding := range findings {
		_, exists := known[finding.ID]
		_, duplicate := byID[finding.ID]
		if !exists || duplicate {
			accepted = false
			reason = "criteria-response-invalid"
			continue
		}
		byID[finding.ID] = finding
	}
	claims := []memory.Claim{}
	evidence := []string{}
	for _, c := range criteria {
		f, exists := byID[c.ID]
		locator, proven := locate(observation.Subject.Locator.Quote, f.Quote, observation.Subject.Locator.Start)
		if !exists || !proven || !ownsQuote(f.Quote, observation.Subject.Locator.Quote, observation.Subject.Name, subjects, false) || (f.Verdict != "match" && f.Verdict != "mismatch") {
			if c.Kind != "should" {
				accepted = false
				reason = "criteria-evidence-missing"
			}
			continue
		}
		if (c.Kind == "must" && f.Verdict != "match") || (c.Kind == "exclude" && f.Verdict != "mismatch") {
			accepted = false
			reason = "criteria-not-satisfied"
		}
		claims = append(claims, memory.Claim{Predicate: fmt.Sprintf("criteria-evidence:%d:%s:%s", criteriaVersion, c.ID, f.Verdict), Value: f.Quote, Original: f.Quote, SubjectKey: observation.Subject.Key, Locator: locator, Knowledge: "observed", Method: "llm-evidence", MethodVersion: MethodVersion})
		evidence = append(evidence, fmt.Sprintf("%s:%d:%s:%s:%d:%d", observation.ObservationID, criteriaVersion, c.ID, f.Verdict, locator.Start, locator.End))
	}
	return accepted, reason, claims, evidence
}
