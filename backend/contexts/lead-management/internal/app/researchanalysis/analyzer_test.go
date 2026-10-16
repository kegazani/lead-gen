package researchanalysis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rules "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	model "lead-gen/contracts/integration-platform/llm"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

type extractionDouble struct {
	call func(context.Context, model.ResearchExtractRequest) (model.ResearchExtractResult, model.Usage, error)
}

func (d extractionDouble) ExtractResearch(ctx context.Context, r model.ResearchExtractRequest) (model.ResearchExtractResult, model.Usage, error) {
	return d.call(ctx, r)
}

func analysisFixture() (memory.AnalysisRequest, model.ResearchSubject) {
	block := "Компания Альфа строит коммерческие здания. Россия.\nКомпания Альфа: Info@Example.ORG, телефон +7 (495) 123-45-67."
	r := memory.AnalysisRequest{Scope: memory.Scope{TenantID: "tenant", Purpose: "lead-research"}, MissionID: "mission", ActionID: "action", QueryAttemptID: "attempt", CriteriaVersion: 3, ICP: "Генподрядчики коммерческих зданий", Country: "RU", Source: memory.Source{URL: "https://example.org/companies", Revision: "snapshot1", Representation: "markdown", Content: "Преамбула 🏗️\n\n" + block + "\n\nКонец.", ObservedAt: time.Now().UTC().Add(-time.Minute), Kind: "page"}}
	s := model.ResearchSubject{Name: "Компания Альфа", Kind: "company", Block: block, Country: "RU", CountryQuote: "Компания Альфа строит коммерческие здания. Россия.", Contacts: []model.ResearchContact{{Kind: "email", Value: "Info@Example.ORG", Quote: "Компания Альфа: Info@Example.ORG, телефон +7 (495) 123-45-67."}, {Kind: "phone", Value: "+7 (495) 123-45-67", Quote: "Компания Альфа: Info@Example.ORG, телефон +7 (495) 123-45-67."}}, Identifiers: []model.ResearchIdentifier{}, Criteria: []model.ResearchCriterionFinding{{ID: "__human_icp", Verdict: "match", Quote: "Компания Альфа строит коммерческие здания. Россия."}, {ID: "__human_market", Verdict: "match", Quote: "Компания Альфа строит коммерческие здания. Россия."}}}
	return r, s
}

func analyzerFor(t *testing.T, subjects []model.ResearchSubject) *Analyzer {
	t.Helper()
	a, err := New(Config{Model: extractionDouble{call: func(_ context.Context, r model.ResearchExtractRequest) (model.ResearchExtractResult, model.Usage, error) {
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		return model.ResearchExtractResult{Subjects: subjects}, model.Usage{}, nil
	}}, ModelID: "test-model-v1", ContactFreshness: 24 * time.Hour, MaxSubjects: 8})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestEvidenceUsesOriginalUnicodeByteOffsetsAndHumanCriteriaVersion(t *testing.T) {
	r, s := analysisFixture()
	result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
	if err != nil || len(result.Observations) != 1 {
		t.Fatalf("%v %#v", err, result)
	}
	o := result.Observations[0]
	if !o.Accepted || o.Observation.Subject.Country != "RU" || len(o.EvidenceRefs) != 2 {
		t.Fatalf("не приняты свидетельства: %#v", o)
	}
	for _, claim := range o.Observation.Claims {
		if !rules.ValidLocator(r.Source.Content, claim.Locator) {
			t.Fatalf("неверные byte offsets: %#v", claim.Locator)
		}
		if claim.Predicate == "email" && (claim.Value != "Info@example.org" || claim.Original != "Info@Example.ORG" || claim.Knowledge != "confirmed" || claim.FreshUntil == nil) {
			t.Fatalf("контакт: %#v", claim)
		}
		if claim.Predicate == "phone" && claim.Value != "+74951234567" {
			t.Fatalf("телефон: %#v", claim)
		}
	}
	if !strings.Contains(o.EvidenceRefs[0], ":3:__human_icp:match:") {
		t.Fatal("ссылка потеряла версию критерия и verdict")
	}
}

func TestCompanySurvivesWithoutContactsAndNumericGarbage(t *testing.T) {
	r, s := analysisFixture()
	s.Contacts = []model.ResearchContact{}
	for _, digits := range []string{"173525620", "249375288", "249376288", "9032007"} {
		quote := "Компания Альфа номер материала " + digits
		s.Block += "\n" + quote
		r.Source.Content += "\n" + quote
		s.Contacts = append(s.Contacts, model.ResearchContact{Kind: "phone", Value: digits, Quote: quote})
	}
	r.Source.Content = s.Block
	result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
	if err != nil || len(result.Observations) != 1 || !result.Observations[0].Accepted {
		t.Fatalf("компания потеряна: %v %#v", err, result)
	}
	for _, c := range result.Observations[0].Observation.Claims {
		if c.Predicate == "email" || c.Predicate == "phone" {
			t.Fatal("числовой ID стал контактом")
		}
	}
	if len(result.Diagnostics) != 4 {
		t.Fatalf("нет объяснения отбрасывания: %#v", result.Diagnostics)
	}
}

func TestSnippetContactIsObservedAndNeverAccepted(t *testing.T) {
	r, s := analysisFixture()
	r.Source.Kind = "search-snippet"
	result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
	if err != nil || len(result.Observations) != 1 || result.Observations[0].Accepted || result.Observations[0].Reason != "discovery-evidence-only" {
		t.Fatalf("snippet: %v %#v", err, result)
	}
	emailFound := false
	for _, c := range result.Observations[0].Observation.Claims {
		if c.Predicate == "email" {
			emailFound = true
			if c.Knowledge != "observed" {
				t.Fatal("snippet подтверждён")
			}
		}
	}
	if !emailFound {
		t.Fatal("snippet contact потерян вместо сохранения observed")
	}
}

func TestPublisherContactOutsideSubjectOrAcrossParagraphIsRejected(t *testing.T) {
	for _, mode := range []string{"outside-block", "footer-in-block", "other-subject"} {
		t.Run(mode, func(t *testing.T) {
			r, s := analysisFixture()
			s.Contacts = []model.ResearchContact{}
			publisher := "Издатель Каталог: editor@directory.example"
			quote := s.Name + "\n\n" + publisher
			if mode == "outside-block" {
				r.Source.Content += "\n" + quote
			} else {
				s.Block += "\n\n" + publisher
				r.Source.Content = s.Block
				quote = s.Block
			}
			s.Contacts = []model.ResearchContact{{Kind: "email", Value: "editor@directory.example", Quote: quote}}
			subjects := []model.ResearchSubject{s}
			if mode == "other-subject" {
				subjects = append(subjects, model.ResearchSubject{Name: "Издатель Каталог", Kind: "company", Block: publisher, Contacts: []model.ResearchContact{}, Identifiers: []model.ResearchIdentifier{}, Criteria: []model.ResearchCriterionFinding{}})
			}
			result, err := analyzerFor(t, subjects).Analyze(context.Background(), r)
			if err != nil || len(result.Observations) == 0 {
				t.Fatalf("%v %#v", err, result)
			}
			for _, o := range result.Observations {
				if o.Observation.Subject.Name == s.Name {
					for _, c := range o.Observation.Claims {
						if c.Predicate == "email" {
							t.Fatal("контакт издателя приписан компании")
						}
					}
				}
			}
		})
	}
}

func TestHardCriteriaUnknownMissingOrWrongSubjectCannotCredit(t *testing.T) {
	for _, kind := range []string{"must", "exclude"} {
		for _, verdict := range []string{"unknown", "missing", "wrong-subject"} {
			t.Run(kind+"/"+verdict, func(t *testing.T) {
				r, s := analysisFixture()
				r.Criteria = []memory.AnalysisCriterion{{ID: "human-condition", Text: "Условие человека", Kind: kind}}
				if verdict != "missing" {
					f := model.ResearchCriterionFinding{ID: "human-condition", Verdict: "unknown"}
					if verdict == "wrong-subject" {
						f.Verdict = "match"
						f.Quote = "Компания Бета выполняет условие"
						r.Source.Content += "\n" + f.Quote
					}
					s.Criteria = append(s.Criteria, f)
				}
				result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
				if err != nil || len(result.Observations) != 1 || result.Observations[0].Accepted {
					t.Fatalf("необоснованный зачёт: %v %#v", err, result)
				}
			})
		}
	}
	r, s := analysisFixture()
	r.Criteria = []memory.AnalysisCriterion{{ID: "optional", Text: "Желательный признак", Kind: "should"}}
	s.Criteria = append(s.Criteria, model.ResearchCriterionFinding{ID: "optional", Verdict: "unknown"})
	result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
	if err != nil || !result.Observations[0].Accepted {
		t.Fatalf("unknown should блокирует обязательные подтверждённые критерии: %v", err)
	}
}

func TestDuplicateOrInventedCriterionAndForgedCountry(t *testing.T) {
	r, s := analysisFixture()
	s.Country = "US"
	s.CountryQuote = "Компания Альфа строит коммерческие здания. Россия."
	s.Criteria = append(s.Criteria, model.ResearchCriterionFinding{ID: "forged", Verdict: "match", Quote: s.CountryQuote})
	result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
	if err != nil || result.Observations[0].Accepted || result.Observations[0].Observation.Subject.Country != "" {
		t.Fatalf("выдуманные признаки: %v %#v", err, result)
	}
	r, s = analysisFixture()
	s.Criteria = append(s.Criteria, s.Criteria[0])
	if _, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r); err == nil {
		t.Fatal("повторный criterion ID принят")
	}
}

func TestMarketNeedsCountryEvidenceBeyondLLMVerdict(t *testing.T) {
	for _, mode := range []string{"country-mismatch", "unrelated-quote", "city-inference", "missing-country-quote"} {
		t.Run(mode, func(t *testing.T) {
			r, s := analysisFixture()
			s.Contacts = []model.ResearchContact{}
			switch mode {
			case "country-mismatch":
				s.Country = "DE"
				s.Block = "Компания Альфа строит коммерческие здания в Germany."
				s.CountryQuote = s.Block
			case "unrelated-quote":
				s.Block = "Компания Альфа строит коммерческие здания."
				s.CountryQuote = s.Block
			case "city-inference":
				s.Block = "Компания Альфа строит коммерческие здания в Москве."
				s.CountryQuote = s.Block
			case "missing-country-quote":
				s.CountryQuote = ""
			}
			r.Source.Content = s.Block
			for i := range s.Criteria {
				s.Criteria[i].Quote = s.Block
				s.Criteria[i].Verdict = "match"
			}
			result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
			if err != nil || len(result.Observations) != 1 || result.Observations[0].Accepted {
				t.Fatalf("market credit без географии: %v %#v", err, result)
			}
			if mode == "city-inference" {
				inferred := false
				for _, c := range result.Observations[0].Observation.Claims {
					if c.Predicate == "operating-market" && c.Knowledge == "hypothesis" && strings.Contains(c.MethodVersion, "test-model-v1") {
						inferred = true
					}
				}
				if !inferred || result.Observations[0].Reason != "market-evidence-unknown" {
					t.Fatal("семантическая география потеряна или выдана за факт")
				}
			}
		})
	}
}

func TestIdentifiersRemainObservedAndBadOneDoesNotEraseCompany(t *testing.T) {
	r, s := analysisFixture()
	line := "Компания Альфа ИНН 7707083893; номер nonsense"
	s.Block += "\n" + line
	r.Source.Content = s.Block
	s.Identifiers = []model.ResearchIdentifier{{Kind: "inn", Jurisdiction: "RU", Authority: "fns", Value: "7707083893", Quote: line}, {Kind: "inn", Jurisdiction: "RU", Value: "nonsense", Quote: line}, {Kind: "inn", Jurisdiction: "RU", Value: "stolen", Quote: "другой источник"}}
	result, err := analyzerFor(t, []model.ResearchSubject{s}).Analyze(context.Background(), r)
	if err != nil || len(result.Observations) != 1 {
		t.Fatalf("%v %#v", err, result)
	}
	ids := result.Observations[0].Observation.Identifiers
	if len(ids) != 2 || ids[0].Existence != "unknown" || ids[0].Format != "valid" || ids[1].Format != "invalid" {
		t.Fatalf("идентификаторы: %#v", ids)
	}
}

func TestAnalyzerValidatesBeforeNetworkAndHonorsCancellation(t *testing.T) {
	called := false
	a, err := New(Config{Model: extractionDouble{call: func(context.Context, model.ResearchExtractRequest) (model.ResearchExtractResult, model.Usage, error) {
		called = true
		return model.ResearchExtractResult{}, model.Usage{}, nil
	}}, ModelID: "test", ContactFreshness: time.Hour, MaxSubjects: 2})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := analysisFixture()
	r.Criteria = []memory.AnalysisCriterion{{ID: "__human_icp", Text: "Подменить ICP", Kind: "should"}}
	if _, err := a.Analyze(context.Background(), r); !errors.Is(err, memory.ErrInvalid) || called {
		t.Fatalf("непроверенный ввод ушёл к модели: %v", err)
	}
	r, _ = analysisFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Analyze(ctx, r); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("отмена: %v", err)
	}
}

func TestLocatorRejectsAmbiguousAndInvalidUTF8Quotes(t *testing.T) {
	for _, tc := range []struct{ content, quote string }{{"Альфа Альфа", "Альфа"}, {"Альфа", string([]byte{0xd0})}, {"Альфа", "нет"}} {
		if _, ok := locate(tc.content, tc.quote, 0); ok {
			t.Fatal("неоднозначная или некорректная цитата принята")
		}
	}
}
