package ports

import (
	"errors"
	"testing"

	"lead-gen/contracts/integration-platform/llm"
)

func TestSearchJobPayloadRoundTrip(t *testing.T) {
	payload := SearchJobPayload{
		Query:           "логистические компании России",
		MaxCredits:      20,
		TargetCompanies: 100,
		MaxResults:      15,
		Plan: &SearchPlan{
			Targets: []PlanTarget{
				{Query: "логистические компании"},
				{URL: "https://example.com/contacts"},
			},
			Limits: PlanLimits{MaxDepth: 2, MaxTimeSeconds: 120},
		},
		Interpretation: &llm.Interpretation{
			TargetLead:  "логистические операторы России",
			NeedSignals: []string{"своя логистика"},
			Exclusions:  []string{"курьерские службы"},
		},
	}
	raw, err := EncodeSearchJobPayload(payload)
	if err != nil {
		t.Fatalf("EncodeSearchJobPayload: %v", err)
	}
	decoded, err := DecodeSearchJobPayload(raw)
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if decoded.Query != payload.Query || decoded.MaxCredits != payload.MaxCredits {
		t.Fatalf("запрос/бюджет: получено %+v, ожидалось %+v", decoded, payload)
	}
	if decoded.TargetCompanies != 100 || decoded.MaxResults != 15 {
		t.Fatalf("цель/лимит поиска: получено %+v, ожидалось targetCompanies=100 maxResults=15", decoded)
	}
	if decoded.Plan == nil {
		t.Fatal("план не восстановлен")
	}
	if len(decoded.Plan.Targets) != 2 {
		t.Fatalf("целей плана: %d, ожидалось 2", len(decoded.Plan.Targets))
	}
	if decoded.Plan.Targets[0].Query != "логистические компании" || decoded.Plan.Targets[1].URL != "https://example.com/contacts" {
		t.Fatalf("цели плана искажены: %+v", decoded.Plan.Targets)
	}
	if decoded.Plan.Limits.MaxDepth != 2 || decoded.Plan.Limits.MaxTimeSeconds != 120 {
		t.Fatalf("лимиты плана искажены: %+v", decoded.Plan.Limits)
	}
	if decoded.Interpretation == nil {
		t.Fatal("интерпретация не восстановлена")
	}
	if decoded.Interpretation.TargetLead != "логистические операторы России" {
		t.Fatalf("целевой лид интерпретации искажён: %+v", decoded.Interpretation)
	}
}

func TestSearchJobPayloadInterpretationOptional(t *testing.T) {
	// Payload без интерпретации читается как «интерпретация не задана».
	raw, err := EncodeSearchJobPayload(SearchJobPayload{Query: "запрос"})
	if err != nil {
		t.Fatalf("EncodeSearchJobPayload: %v", err)
	}
	decoded, err := DecodeSearchJobPayload(raw)
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if decoded.Interpretation != nil {
		t.Fatalf("Interpretation без поля должна быть nil, получено %+v", decoded.Interpretation)
	}
}

func TestSearchJobPayloadBackwardCompatible(t *testing.T) {
	// Payload старого формата: запрос, бюджет и план со старыми операторскими
	// лимитами (maxPages/includePaths/excludePaths). Лишние поля должны
	// игнорироваться без ошибки, а глубина — читаться.
	decoded, err := DecodeSearchJobPayload([]byte(`{"query":"старый запрос","maxCredits":5,"plan":{"targets":[{"query":"q"}],"limits":{"maxPages":8,"maxDepth":2,"includePaths":["contact"],"excludePaths":["blog"]}}}`))
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if decoded.Query != "старый запрос" {
		t.Fatalf("Query: получено %q", decoded.Query)
	}
	if decoded.MaxCredits != 5 {
		t.Fatalf("MaxCredits: получено %d, ожидалось 5", decoded.MaxCredits)
	}
	if decoded.Plan == nil {
		t.Fatal("план не восстановлен из старого payload")
	}
	if decoded.Plan.Limits.MaxDepth != 2 || decoded.Plan.Limits.MaxTimeSeconds != 0 {
		t.Fatalf("лимиты старого плана: получено %+v", decoded.Plan.Limits)
	}
}

func TestSearchJobPayloadWithoutOptionalFields(t *testing.T) {
	// Payload самого старого формата: только запрос.
	decoded, err := DecodeSearchJobPayload([]byte(`{"query":"старый запрос"}`))
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if decoded.Query != "старый запрос" {
		t.Fatalf("Query: получено %q", decoded.Query)
	}
	if decoded.MaxCredits != 0 {
		t.Fatalf("MaxCredits без поля: получено %d, ожидался 0", decoded.MaxCredits)
	}
	if decoded.TargetCompanies != 0 {
		t.Fatalf("TargetCompanies без поля: получено %d, ожидался 0", decoded.TargetCompanies)
	}
	if decoded.MaxResults != 0 {
		t.Fatalf("MaxResults без поля: получено %d, ожидался 0", decoded.MaxResults)
	}
	if decoded.Plan != nil {
		t.Fatalf("Plan без поля должен быть nil, получено %+v", decoded.Plan)
	}
	if decoded.Interpretation != nil {
		t.Fatalf("Interpretation без поля должна быть nil, получено %+v", decoded.Interpretation)
	}
}

func TestValidateTargetCompanies(t *testing.T) {
	if err := ValidateTargetCompanies(0); err != nil {
		t.Fatalf("ноль должен быть допустим: %v", err)
	}
	if err := ValidateTargetCompanies(100); err != nil {
		t.Fatalf("положительное значение должно быть допустимо: %v", err)
	}
	if err := ValidateTargetCompanies(-1); !errors.Is(err, ErrInvalidTargetCompanies) {
		t.Fatalf("отрицательное значение: ошибка %v не удовлетворяет ErrInvalidTargetCompanies", err)
	}
}

func TestSearchPlanHasTargets(t *testing.T) {
	if (SearchPlan{}).HasTargets() {
		t.Fatal("пустой план не должен иметь целей")
	}
	if !(SearchPlan{Targets: []PlanTarget{{Query: "q"}}}).HasTargets() {
		t.Fatal("план с целью должен сообщать о наличии целей")
	}
}

func TestSearchPlanValidate(t *testing.T) {
	cases := []struct {
		name    string
		plan    SearchPlan
		wantErr bool
	}{
		{name: "пустой план допустим", plan: SearchPlan{}},
		{name: "цели и лимиты", plan: SearchPlan{Targets: []PlanTarget{{Query: "q"}, {URL: "https://example.com"}}, Limits: PlanLimits{MaxDepth: 2, MaxTimeSeconds: 120}}},
		{name: "цель и query, и url", plan: SearchPlan{Targets: []PlanTarget{{Query: "q", URL: "https://example.com"}}}, wantErr: true},
		{name: "цель без query и url", plan: SearchPlan{Targets: []PlanTarget{{}}}, wantErr: true},
		{name: "отрицательная глубина", plan: SearchPlan{Limits: PlanLimits{MaxDepth: -1}}, wantErr: true},
		{name: "отрицательное время", plan: SearchPlan{Limits: PlanLimits{MaxTimeSeconds: -1}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.plan.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.plan)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}
