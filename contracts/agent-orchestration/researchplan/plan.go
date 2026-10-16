// Package researchplan публикует версионированное человеческое задание и план
// исследования. Содержимое хранится в red-зоне; green получает только ссылки.
package researchplan

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	OriginHuman            = "human"
	OriginInferred         = "inferred"
	OriginDefault          = "default"
	OriginRecipe           = "recipe"
	UnitCompany            = "company"
	UnitCompanyWithContact = "company-with-contact"
	UnitEmailReady         = "email-ready"
	SchemaVersion          = "research-plan/1"
	PromptVersion          = "discovery-portfolio/1"

	// Технические границы текущего пути анализа. Два дополнительных места
	// нужны для человеческого ICP и выбранного рынка. Критерии не обрезаются.
	MaxEvaluatedCriteria = 38
	MaxCriteriaPerGroup  = 32
	MaxICPBytes          = 16000
	MaxCriterionIDBytes  = 128
	MaxScopeIDBytes      = 200
)

// Criterion сохраняет происхождение; гипотеза не превращается в ограничение.
type Criterion struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	Origin       string   `json:"origin"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type CriteriaSnapshot struct {
	Version int         `json:"version"`
	Must    []Criterion `json:"must,omitempty"`
	Should  []Criterion `json:"should,omitempty"`
	Exclude []Criterion `json:"exclude,omitempty"`
	Unknown []Criterion `json:"unknown,omitempty"`
}

type MarketSelection struct {
	Country        string   `json:"country"`
	Languages      []string `json:"languages"`
	Origin         string   `json:"origin"`
	ProfileVersion string   `json:"profile_version"`
}

// Goal не имеет значения по умолчанию. Количество и единица задаются человеком.
type Goal struct {
	Quantity int    `json:"quantity"`
	Unit     string `json:"unit"`
}

type SearchBrief struct {
	ID            string            `json:"id"`
	Revision      int               `json:"revision"`
	TenantID      string            `json:"tenant_id"`
	Purpose       string            `json:"purpose"`
	RequestID     string            `json:"request_id"`
	IntentVersion int               `json:"intent_version"`
	ActorID       string            `json:"actor_id"`
	Offer         string            `json:"offer"`
	ICP           string            `json:"icp"`
	Criteria      CriteriaSnapshot  `json:"criteria"`
	Markets       []MarketSelection `json:"markets"`
	Goal          Goal              `json:"goal"`
}

// NewBrief применяется только при создании новой заявки, не при чтении legacy.
// Нормализация копирует срезы, чтобы вызывающая сторона не меняла снимок.
func NewBrief(in SearchBrief) (SearchBrief, error) {
	b := in.Clone()
	if b.Revision == 0 {
		b.Revision = 1
	}
	if b.Criteria.Version == 0 {
		b.Criteria.Version = 1
	}
	if len(b.Markets) == 0 {
		b.Markets = []MarketSelection{{Country: "RU", Languages: []string{"ru"}, Origin: OriginDefault, ProfileVersion: CountryCatalogVersion}}
	}
	for i := range b.Markets {
		m := &b.Markets[i]
		m.Country = strings.ToUpper(strings.TrimSpace(m.Country))
		if m.Country == "UK" {
			m.Country = "GB"
		}
		if m.Origin == "" {
			m.Origin = OriginHuman
		}
		if m.ProfileVersion == "" {
			m.ProfileVersion = CountryCatalogVersion
		}
		if len(m.Languages) == 0 {
			if p, ok := LookupCountry(m.Country); ok {
				m.Languages = append([]string(nil), p.DefaultLanguages...)
			}
		}
	}
	return b, b.Validate()
}

func (b SearchBrief) Clone() SearchBrief {
	b.Criteria.Must = cloneCriteria(b.Criteria.Must)
	b.Criteria.Should = cloneCriteria(b.Criteria.Should)
	b.Criteria.Exclude = cloneCriteria(b.Criteria.Exclude)
	b.Criteria.Unknown = cloneCriteria(b.Criteria.Unknown)
	b.Markets = append([]MarketSelection(nil), b.Markets...)
	for i := range b.Markets {
		b.Markets[i].Languages = append([]string(nil), b.Markets[i].Languages...)
	}
	return b
}

func cloneCriteria(in []Criterion) []Criterion {
	out := append([]Criterion(nil), in...)
	for i := range out {
		out[i].EvidenceRefs = append([]string(nil), out[i].EvidenceRefs...)
	}
	return out
}

func (b SearchBrief) Validate() error {
	for name, value := range map[string]string{"id": b.ID, "tenant_id": b.TenantID, "purpose": b.Purpose, "request_id": b.RequestID, "actor_id": b.ActorID} {
		if !validText(value, 256) {
			return fmt.Errorf("researchplan: %s обязателен и ограничен 256 символами", name)
		}
	}
	if !validText(b.Offer, 8000) || !validText(b.ICP, 8000) {
		return fmt.Errorf("researchplan: offer и icp обязательны, не более 8000 символов каждый")
	}
	if len(b.TenantID) > MaxScopeIDBytes || len(b.Purpose) > MaxScopeIDBytes {
		return fmt.Errorf("researchplan: tenant_id и purpose ограничены %d байтами UTF-8", MaxScopeIDBytes)
	}
	if len(b.ICP) > MaxICPBytes {
		return fmt.Errorf("researchplan: icp превышает %d байт UTF-8", MaxICPBytes)
	}
	if b.Revision < 1 || b.IntentVersion < 1 || b.Criteria.Version < 1 {
		return fmt.Errorf("researchplan: версии должны быть положительными")
	}
	if b.Goal.Quantity < 1 {
		return fmt.Errorf("researchplan: количество задаётся человеком и должно быть положительным")
	}
	switch b.Goal.Unit {
	case UnitCompany, UnitCompanyWithContact, UnitEmailReady:
	default:
		return fmt.Errorf("researchplan: неизвестная единица результата")
	}
	if len(b.Markets) < 1 || len(b.Markets) > 32 {
		return fmt.Errorf("researchplan: требуется от 1 до 32 рынков")
	}
	seenCountries := map[string]bool{}
	for _, m := range b.Markets {
		if _, ok := LookupCountry(m.Country); !ok || seenCountries[m.Country] {
			return fmt.Errorf("researchplan: неизвестная или повторная страна %q", m.Country)
		}
		seenCountries[m.Country] = true
		if m.Origin != OriginHuman && m.Origin != OriginDefault {
			return fmt.Errorf("researchplan: рынок может задавать только человек или документированный default")
		}
		if m.ProfileVersion != CountryCatalogVersion {
			return fmt.Errorf("researchplan: неизвестная версия профиля страны")
		}
		if len(m.Languages) < 1 || len(m.Languages) > 8 {
			return fmt.Errorf("researchplan: для %s требуется явный язык", m.Country)
		}
		seenLanguages := map[string]bool{}
		for _, lang := range m.Languages {
			if !ValidLanguage(lang) || seenLanguages[lang] {
				return fmt.Errorf("researchplan: неизвестный или повторный язык %q", lang)
			}
			seenLanguages[lang] = true
		}
	}
	if len(b.Criteria.Must)+len(b.Criteria.Should)+len(b.Criteria.Exclude) > MaxEvaluatedCriteria {
		return fmt.Errorf("researchplan: суммарно допускается не более %d оцениваемых критериев must/should/exclude", MaxEvaluatedCriteria)
	}
	seenCriteria := map[string]bool{}
	for _, group := range []struct {
		list []Criterion
		hard bool
	}{{b.Criteria.Must, true}, {b.Criteria.Should, false}, {b.Criteria.Exclude, true}, {b.Criteria.Unknown, false}} {
		if len(group.list) > MaxCriteriaPerGroup {
			return fmt.Errorf("researchplan: слишком много критериев")
		}
		for _, c := range group.list {
			if !validText(c.ID, MaxCriterionIDBytes) || len(c.ID) > MaxCriterionIDBytes || strings.HasPrefix(c.ID, "__human_") || !validText(c.Text, 1000) || seenCriteria[c.ID] {
				return fmt.Errorf("researchplan: некорректный или повторный критерий")
			}
			seenCriteria[c.ID] = true
			if c.Origin != OriginHuman && c.Origin != OriginInferred {
				return fmt.Errorf("researchplan: происхождение критерия обязательно")
			}
			if group.hard && c.Origin != OriginHuman {
				return fmt.Errorf("researchplan: модель не может добавлять обязательные критерии или исключения")
			}
			if len(c.EvidenceRefs) > 16 {
				return fmt.Errorf("researchplan: слишком много ссылок критерия")
			}
			for _, ref := range c.EvidenceRefs {
				if !validText(ref, 256) {
					return fmt.Errorf("researchplan: некорректная ссылка критерия")
				}
			}
		}
	}
	return nil
}

// SourceCapability задаётся конфигурацией адаптера, не моделью или профилем страны.
// "*" допустим только в явном capability manifest провайдера.
type SourceCapability struct {
	ID           string   `json:"id"`
	Revision     string   `json:"revision"`
	SourceFamily string   `json:"source_family"`
	Countries    []string `json:"countries"`
	Languages    []string `json:"languages"`
	Query        bool     `json:"query"`
	Available    bool     `json:"available"`
}

func (s SourceCapability) Supports(country, language string) bool {
	return s.Available && s.Query && includes(s.Countries, country) && includes(s.Languages, language)
}

func (s SourceCapability) Validate() error {
	if !validText(s.ID, 128) || !validText(s.Revision, 128) {
		return fmt.Errorf("researchplan: источник требует id и revision")
	}
	if !validSourceFamily(s.SourceFamily) {
		return fmt.Errorf("researchplan: неизвестное семейство источника")
	}
	if len(s.Countries) == 0 || len(s.Languages) == 0 {
		return fmt.Errorf("researchplan: capability требует явную географию и языки")
	}
	for _, c := range s.Countries {
		if c != "*" {
			if _, ok := LookupCountry(c); !ok {
				return fmt.Errorf("researchplan: неизвестная страна источника %q", c)
			}
		}
	}
	for _, l := range s.Languages {
		if l != "*" && !ValidLanguage(l) {
			return fmt.Errorf("researchplan: неизвестный язык источника %q", l)
		}
	}
	return nil
}

func validSourceFamily(value string) bool {
	switch value {
	case "web", "directory", "association", "registry", "news":
		return true
	}
	return false
}

func includes(values []string, want string) bool {
	for _, value := range values {
		if value == want || value == "*" {
			return true
		}
	}
	return false
}

type QueryFamily struct {
	ID           string   `json:"id"`
	Country      string   `json:"country"`
	Language     string   `json:"language"`
	SourceFamily string   `json:"source_family"`
	Question     string   `json:"question"`
	Query        string   `json:"query"`
	Origin       string   `json:"origin"`
	CriteriaRefs []string `json:"criteria_refs,omitempty"`
}

type StrategyCell struct {
	ID             string `json:"id"`
	Country        string `json:"country"`
	Language       string `json:"language"`
	Segment        string `json:"segment"`
	SourceID       string `json:"source_id"`
	SourceRevision string `json:"source_revision"`
	SourceFamily   string `json:"source_family"`
	Question       string `json:"question"`
	QueryFamilyID  string `json:"query_family_id"`
}

type Diagnostic struct {
	Code   string `json:"code"`
	Market string `json:"market,omitempty"`
	Detail string `json:"detail"`
}

// Usage — необязательная диагностика proxy, не денежный бюджет приложения.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type SearchStrategyPlan struct {
	ID            string         `json:"id"`
	Revision      int            `json:"revision"`
	Brief         SearchBrief    `json:"brief"`
	BriefID       string         `json:"brief_id"`
	BriefRevision int            `json:"brief_revision"`
	TenantID      string         `json:"tenant_id"`
	Purpose       string         `json:"purpose"`
	PromptVersion string         `json:"prompt_version"`
	SchemaVersion string         `json:"schema_version"`
	ModelID       string         `json:"model_id,omitempty"`
	PlannerStatus string         `json:"planner_status"`
	Families      []QueryFamily  `json:"families"`
	Cells         []StrategyCell `json:"cells"`
	Diagnostics   []Diagnostic   `json:"diagnostics,omitempty"`
	ModelCalls    int            `json:"model_calls"`
	Usage         Usage          `json:"usage"`
}

type Plan = SearchStrategyPlan

type Planner interface {
	Plan(context.Context, SearchBrief) (SearchStrategyPlan, error)
}

// Validate проверяет сохранённый план повторно перед исполнением.
func (p SearchStrategyPlan) Validate() error {
	if err := p.Brief.Validate(); err != nil {
		return err
	}
	if !validText(p.ID, 128) || p.Revision < 1 || p.BriefID != p.Brief.ID || p.BriefRevision != p.Brief.Revision || p.TenantID != p.Brief.TenantID || p.Purpose != p.Brief.Purpose || p.SchemaVersion != SchemaVersion || p.PromptVersion != PromptVersion {
		return fmt.Errorf("researchplan: план не соответствует версии и области поручения")
	}
	switch p.PlannerStatus {
	case "llm", "partial", "recipe", "model-unavailable", "model-rejected", "no-capability":
	default:
		return fmt.Errorf("researchplan: неизвестное состояние планировщика")
	}
	if len(p.Cells) > 256 || len(p.Families) > 256 || len(p.Cells) != len(p.Families) || p.ModelCalls < 0 {
		return fmt.Errorf("researchplan: некорректные размеры плана")
	}
	families := map[string]QueryFamily{}
	criteria := map[string]bool{}
	for _, list := range [][]Criterion{p.Brief.Criteria.Must, p.Brief.Criteria.Should, p.Brief.Criteria.Exclude, p.Brief.Criteria.Unknown} {
		for _, c := range list {
			criteria[c.ID] = true
		}
	}
	for _, f := range p.Families {
		if !validText(f.ID, 128) || !validText(f.Question, 1000) || !ValidQuery(f.Query) || !validSourceFamily(f.SourceFamily) || !marketAllowed(p.Brief.Markets, f.Country, f.Language) {
			return fmt.Errorf("researchplan: некорректное семейство запроса")
		}
		if _, exists := families[f.ID]; exists {
			return fmt.Errorf("researchplan: повторное семейство")
		}
		if f.Origin != OriginInferred && f.Origin != OriginRecipe {
			return fmt.Errorf("researchplan: неизвестное происхождение запроса")
		}
		for _, ref := range f.CriteriaRefs {
			if !criteria[ref] {
				return fmt.Errorf("researchplan: выдуманная ссылка на критерий")
			}
		}
		families[f.ID] = f
	}
	cells := map[string]bool{}
	used := map[string]bool{}
	for _, c := range p.Cells {
		f, ok := families[c.QueryFamilyID]
		if !ok || used[c.QueryFamilyID] || cells[c.ID] || !validText(c.ID, 128) || !validText(c.SourceID, 128) || !validText(c.SourceRevision, 128) || !validText(c.Segment, 8000) || c.Country != f.Country || c.Language != f.Language || c.SourceFamily != f.SourceFamily || c.Question != f.Question {
			return fmt.Errorf("researchplan: участок исследования не соответствует семейству")
		}
		cells[c.ID] = true
		used[c.QueryFamilyID] = true
	}
	return nil
}

// ValidateSources проверяет доступность и закреплённую ревизию перед выдачей
// действия. Наличие старого сохранённого плана не возвращает отозванный доступ.
func (p SearchStrategyPlan) ValidateSources(sources []SourceCapability) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for _, cell := range p.Cells {
		found := false
		for _, source := range sources {
			if source.ID == cell.SourceID && source.Revision == cell.SourceRevision && source.SourceFamily == cell.SourceFamily && source.Supports(cell.Country, cell.Language) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("researchplan: источник или ревизия недоступны для участка %s", cell.ID)
		}
	}
	return nil
}

func marketAllowed(markets []MarketSelection, country, language string) bool {
	for _, m := range markets {
		if m.Country == country {
			for _, l := range m.Languages {
				if l == language {
					return true
				}
			}
		}
	}
	return false
}

// ValidQuery проверяет данные запроса, не исполняя URL, команды или инструкции.
func ValidQuery(q string) bool {
	if !validText(q, 512) {
		return false
	}
	for _, r := range q {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validText(value string, max int) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
