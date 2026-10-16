package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const ResearchExtractionSchemaVersion = "research-extraction/1"

var ErrResearchOutputTruncated = errors.New("llm: ответ извлечения усечён техническим пределом")

// ResearchExtraction — отдельная способность model gateway: наблюдения из
// сохранённой страницы. Ответ модели ещё не является подтверждённым фактом.
// Существующий Port не расширяется, чтобы legacy адаптеры не обещали эту функцию.
type ResearchExtraction interface {
	ExtractResearch(context.Context, ResearchExtractRequest) (ResearchExtractResult, Usage, error)
}

type ResearchCriterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Kind string `json:"kind"`
}

type ResearchExtractRequest struct {
	URL            string              `json:"url"`
	Representation string              `json:"representation"`
	Content        string              `json:"content"`
	ICP            string              `json:"icp"`
	Country        string              `json:"country"`
	Criteria       []ResearchCriterion `json:"criteria"`
	MaxSubjects    int                 `json:"max_subjects"`
}

func (r ResearchExtractRequest) Validate() error {
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || len(r.URL) > 8000 || strings.TrimSpace(r.Content) == "" || !utf8.ValidString(r.Content) || len(r.Content) > 256<<10 || r.MaxSubjects < 1 || r.MaxSubjects > 32 || len(r.Criteria) > 40 || strings.TrimSpace(r.ICP) == "" || len(r.ICP) > 16000 {
		return fmt.Errorf("llm: некорректные пределы извлечения исследования")
	}
	if r.Representation != "markdown" && r.Representation != "text" {
		return fmt.Errorf("llm: неподдерживаемое представление исследования")
	}
	if !researchCountryCode(r.Country) {
		return fmt.Errorf("llm: некорректный код рынка")
	}
	seen := map[string]bool{}
	for _, c := range r.Criteria {
		if c.ID == "" || len(c.ID) > 128 || strings.TrimSpace(c.Text) == "" || len(c.Text) > 16000 || seen[c.ID] || (c.Kind != "must" && c.Kind != "should" && c.Kind != "exclude") {
			return fmt.Errorf("llm: некорректный критерий исследования")
		}
		seen[c.ID] = true
	}
	return nil
}

type ResearchContact struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Quote string `json:"quote"`
}

type ResearchIdentifier struct {
	Kind         string `json:"kind"`
	Jurisdiction string `json:"jurisdiction"`
	Authority    string `json:"authority"`
	Value        string `json:"value"`
	Quote        string `json:"quote"`
}

type ResearchCriterionFinding struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Quote   string `json:"quote"`
}

type ResearchSubject struct {
	Name         string                     `json:"name"`
	Kind         string                     `json:"kind"`
	Block        string                     `json:"block"`
	Country      string                     `json:"country"`
	CountryQuote string                     `json:"country_quote"`
	Contacts     []ResearchContact          `json:"contacts"`
	Identifiers  []ResearchIdentifier       `json:"identifiers"`
	Criteria     []ResearchCriterionFinding `json:"criteria"`
}

type ResearchExtractResult struct {
	Subjects []ResearchSubject `json:"subjects"`
}

// ValidateBounds проверяет схему значений и технические размеры. Присутствие
// цитаты в документе и принадлежность субъекту проверяются владельцем памяти.
func (r ResearchExtractResult) ValidateBounds(maxSubjects int) error {
	if r.Subjects == nil || len(r.Subjects) > maxSubjects {
		return fmt.Errorf("llm: недопустимое число извлечённых субъектов")
	}
	for _, s := range r.Subjects {
		if !boundedResearchText(s.Name, 500) || !boundedResearchText(s.Block, 6000) || !researchCountryCode(s.Country) || !optionalResearchText(s.CountryQuote, 1000) || s.Contacts == nil || s.Identifiers == nil || s.Criteria == nil || len(s.Contacts) > 32 || len(s.Identifiers) > 20 || len(s.Criteria) > 40 {
			return fmt.Errorf("llm: субъект вне схемы или технических пределов")
		}
		switch s.Kind {
		case "company", "legal-entity", "branch", "brand":
		default:
			return fmt.Errorf("llm: неизвестный вид субъекта")
		}
		for _, c := range s.Contacts {
			if (c.Kind != "email" && c.Kind != "phone") || !boundedResearchText(c.Value, 500) || !boundedResearchText(c.Quote, 1000) {
				return fmt.Errorf("llm: контакт вне схемы")
			}
		}
		for _, id := range s.Identifiers {
			if !boundedResearchText(id.Kind, 64) || !researchCountryCode(id.Jurisdiction) || !optionalResearchText(id.Authority, 128) || !boundedResearchText(id.Value, 500) || !boundedResearchText(id.Quote, 1000) {
				return fmt.Errorf("llm: идентификатор вне схемы")
			}
		}
		seen := map[string]bool{}
		for _, f := range s.Criteria {
			if !boundedResearchText(f.ID, 128) || seen[f.ID] || !optionalResearchText(f.Quote, 6000) || (f.Verdict != "match" && f.Verdict != "mismatch" && f.Verdict != "unknown") {
				return fmt.Errorf("llm: результат критерия вне схемы")
			}
			seen[f.ID] = true
		}
	}
	return nil
}

func researchCountryCode(value string) bool {
	return value == "" || (len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z')
}

func boundedResearchText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && optionalResearchText(value, max)
}
func optionalResearchText(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
}
