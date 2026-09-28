package llm

import (
	"encoding/json"
	"testing"
)

func TestPlanRequestValidate(t *testing.T) {
	if err := (PlanRequest{Query: "поставщики упаковки"}).Validate(); err != nil {
		t.Fatalf("валидный запрос отвергнут: %v", err)
	}
	if err := (PlanRequest{}).Validate(); err == nil {
		t.Fatal("запрос без query должен быть отвергнут")
	}
}

// TestPlanRequestValidateWithInterpretation проверяет, что заполненная
// интерпретация не мешает валидному запросу, а пустой query отвергается даже при
// заданных критериях.
func TestPlanRequestValidateWithInterpretation(t *testing.T) {
	interpretation := Interpretation{
		TargetLead:  "логистические компании",
		NeedSignals: []string{"нужна автоматизация"},
		Exclusions:  []string{"физлица"},
	}
	if err := (PlanRequest{Query: "нужны логистические компании", Interpretation: interpretation}).Validate(); err != nil {
		t.Fatalf("запрос с интерпретацией отвергнут: %v", err)
	}
	if err := (PlanRequest{Interpretation: interpretation}).Validate(); err == nil {
		t.Fatal("запрос без query должен быть отвергнут даже с интерпретацией")
	}
}

func TestTargetValidate(t *testing.T) {
	cases := []struct {
		name    string
		target  Target
		wantErr bool
	}{
		{name: "только query", target: Target{Query: "q"}},
		{name: "только url", target: Target{URL: "https://example.com"}},
		{name: "оба", target: Target{Query: "q", URL: "u"}, wantErr: true},
		{name: "ни одного", target: Target{}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.target.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

func TestSuggestRequestValidate(t *testing.T) {
	if err := (SuggestRequest{Description: "поставщики упаковки для маркетплейсов"}).Validate(); err != nil {
		t.Fatalf("валидный запрос отвергнут: %v", err)
	}
	if err := (SuggestRequest{}).Validate(); err == nil {
		t.Fatal("запрос без description должен быть отвергнут")
	}
	if err := (SuggestRequest{Description: "   "}).Validate(); err == nil {
		t.Fatal("запрос с пробелами должен быть отвергнут")
	}
}

func TestSuggestionValidate(t *testing.T) {
	cases := []struct {
		name       string
		suggestion Suggestion
		wantErr    bool
	}{
		{name: "полный", suggestion: Suggestion{Sector: "Логистика", Niche: "Сборные грузы", SeedQueries: []string{"перевозки сборных грузов"}}},
		{name: "без сектора", suggestion: Suggestion{Niche: "Сборные грузы", SeedQueries: []string{"q"}}, wantErr: true},
		{name: "без ниши", suggestion: Suggestion{Sector: "Логистика", SeedQueries: []string{"q"}}, wantErr: true},
		{name: "без seed-запросов", suggestion: Suggestion{Sector: "Логистика", Niche: "Сборные грузы"}, wantErr: true},
		{name: "пустые seed-запросы", suggestion: Suggestion{Sector: "Логистика", Niche: "Сборные грузы", SeedQueries: []string{"  "}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.suggestion.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.suggestion)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

func TestMessageValidate(t *testing.T) {
	cases := []struct {
		name    string
		message Message
		wantErr bool
	}{
		{name: "оператор", message: Message{Role: RoleUser, Content: "ищем логистов"}},
		{name: "агент", message: Message{Role: RoleAssistant, Content: "какой регион?"}},
		{name: "неизвестная роль", message: Message{Role: "system", Content: "текст"}, wantErr: true},
		{name: "пустой текст", message: Message{Role: RoleUser}, wantErr: true},
		{name: "текст из пробелов", message: Message{Role: RoleUser, Content: "   "}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.message.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.message)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

func TestConverseRequestValidate(t *testing.T) {
	valid := ConverseRequest{History: []Message{{Role: RoleUser, Content: "ищем логистов"}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("валидная история отвергнута: %v", err)
	}
	if err := (ConverseRequest{}).Validate(); err == nil {
		t.Fatal("пустая история должна быть отвергнута")
	}
	lastAssistant := ConverseRequest{History: []Message{{Role: RoleAssistant, Content: "какой регион?"}}}
	if err := lastAssistant.Validate(); err == nil {
		t.Fatal("история, заканчивающаяся репликой агента, должна быть отвергнута")
	}
	badMessage := ConverseRequest{History: []Message{{Role: "system", Content: "текст"}}}
	if err := badMessage.Validate(); err == nil {
		t.Fatal("история с неизвестной ролью должна быть отвергнута")
	}
}

func TestConverseReplyValidate(t *testing.T) {
	if err := (ConverseReply{Text: "какой регион?"}).Validate(); err != nil {
		t.Fatalf("валидная реплика отвергнута: %v", err)
	}
	withDraft := ConverseReply{Text: "вот черновик", Targets: []Target{{Query: "q"}}, Ready: true}
	if err := withDraft.Validate(); err != nil {
		t.Fatalf("валидный черновик отвергнут: %v", err)
	}
	if err := (ConverseReply{}).Validate(); err == nil {
		t.Fatal("реплика без текста должна быть отвергнута")
	}
	badTarget := ConverseReply{Text: "план", Targets: []Target{{}}}
	if err := badTarget.Validate(); err == nil {
		t.Fatal("черновик с некорректной целью должен быть отвергнут")
	}
	withLimits := ConverseReply{Text: "план", Limits: PlanLimits{MaxDepth: 2, MaxTimeSeconds: 120}}
	if err := withLimits.Validate(); err != nil {
		t.Fatalf("валидные лимиты отвергнуты: %v", err)
	}
	badLimits := ConverseReply{Text: "план", Limits: PlanLimits{MaxDepth: -1}}
	if err := badLimits.Validate(); err == nil {
		t.Fatal("отрицательная глубина должна быть отвергнута")
	}
}

func TestInterpretRequestValidate(t *testing.T) {
	if err := (InterpretRequest{Query: "нужны логистические компании"}).Validate(); err != nil {
		t.Fatalf("валидный запрос отвергнут: %v", err)
	}
	if err := (InterpretRequest{}).Validate(); err == nil {
		t.Fatal("запрос без query должен быть отвергнут")
	}
	if err := (InterpretRequest{Query: "   "}).Validate(); err == nil {
		t.Fatal("запрос из пробелов должен быть отвергнут")
	}
}

func TestInterpretationValidate(t *testing.T) {
	valid := Interpretation{TargetLead: "логистические компании", NeedSignals: []string{"нужна автоматизация"}, Exclusions: []string{"физлица"}, HypothesisSignals: []string{"отрасль совпадает"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("валидная интерпретация отвергнута: %v", err)
	}
	uncertain := Interpretation{Uncertainty: true}
	if err := uncertain.Validate(); err != nil {
		t.Fatalf("явная неопределённость должна быть допустима: %v", err)
	}

	cases := []struct {
		name           string
		interpretation Interpretation
		wantErr        bool
	}{
		{name: "без целевого лида", interpretation: Interpretation{NeedSignals: []string{"q"}}, wantErr: true},
		{name: "без признаков потребности", interpretation: Interpretation{TargetLead: "лид"}, wantErr: true},
		{name: "пустые признаки потребности", interpretation: Interpretation{TargetLead: "лид", NeedSignals: []string{"  "}}, wantErr: true},
		{name: "неопределённость без критериев", interpretation: Interpretation{TargetLead: "", NeedSignals: nil, Uncertainty: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.interpretation.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.interpretation)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

// TestInterpretationValidateIgnoresReason проверяет, что необязательный Reason
// (в том числе пустой) не влияет на валидность интерпретации.
func TestInterpretationValidateIgnoresReason(t *testing.T) {
	valid := Interpretation{TargetLead: "лид", NeedSignals: []string{"сигнал"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("валидная интерпретация с пустым reason отвергнута: %v", err)
	}
	uncertain := Interpretation{Uncertainty: true, Reason: "сбой вывода интерпретации"}
	if err := uncertain.Validate(); err != nil {
		t.Fatalf("неопределённая интерпретация с reason отвергнута: %v", err)
	}
}

// TestInterpretationJSONRoundTrip проверяет сериализацию и десериализацию
// интерпретации с Reason и без него.
func TestInterpretationJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Interpretation
	}{
		{
			name: "с reason",
			in: Interpretation{
				TargetLead:  "лид",
				NeedSignals: []string{"сигнал"},
				Uncertainty: true,
				Reason:      "сбой вывода интерпретации",
			},
		},
		{
			name: "без reason",
			in:   Interpretation{TargetLead: "лид", NeedSignals: []string{"сигнал"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var out Interpretation
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if out.TargetLead != tc.in.TargetLead || out.Uncertainty != tc.in.Uncertainty || out.Reason != tc.in.Reason {
				t.Fatalf("round-trip: получено %+v, ожидалось %+v", out, tc.in)
			}
			if len(out.NeedSignals) != len(tc.in.NeedSignals) {
				t.Fatalf("round-trip need_signals: получено %v, ожидалось %v", out.NeedSignals, tc.in.NeedSignals)
			}
		})
	}
}

func TestAssessRequestValidate(t *testing.T) {
	if err := (AssessRequest{JobID: "job-1"}).Validate(); err != nil {
		t.Fatalf("валидный запрос отвергнут: %v", err)
	}
	if err := (AssessRequest{}).Validate(); err == nil {
		t.Fatal("запрос без job_id должен быть отвергнут")
	}
	if err := (AssessRequest{JobID: "   "}).Validate(); err == nil {
		t.Fatal("job_id из пробелов должен быть отвергнут")
	}
}

func TestAssessmentEvidenceValidate(t *testing.T) {
	valid := AssessmentEvidence{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalIndirect}
	if err := valid.Validate(); err != nil {
		t.Fatalf("валидное свидетельство отвергнуто: %v", err)
	}
	cases := []struct {
		name     string
		evidence AssessmentEvidence
		wantErr  bool
	}{
		{name: "без критерия", evidence: AssessmentEvidence{Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalDirect}, wantErr: true},
		{name: "без факта", evidence: AssessmentEvidence{Criterion: "c", SourceURL: "u", Reasoning: "r", SignalKind: SignalDirect}, wantErr: true},
		{name: "без источника", evidence: AssessmentEvidence{Criterion: "c", Fact: "f", Reasoning: "r", SignalKind: SignalDirect}, wantErr: true},
		{name: "без обоснования", evidence: AssessmentEvidence{Criterion: "c", Fact: "f", SourceURL: "u", SignalKind: SignalDirect}, wantErr: true},
		{name: "неизвестный тип сигнала", evidence: AssessmentEvidence{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: "unknown"}, wantErr: true},
		{name: "без типа сигнала", evidence: AssessmentEvidence{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.evidence.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.evidence)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

func TestAssessmentValidate(t *testing.T) {
	evaluate := func(t *testing.T, a Assessment) {
		t.Helper()
		if err := a.Validate(); err != nil {
			t.Fatalf("валидная оценка отвергнута: %v", err)
		}
	}

	evaluate(t, Assessment{
		Verdict:    VerdictFits,
		SignalKind: SignalDirect,
		Evidence:   []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalDirect}},
	})
	evaluate(t, Assessment{
		Verdict:    VerdictFits,
		SignalKind: SignalIndirect,
		Evidence:   []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalIndirect}},
	})
	evaluate(t, Assessment{
		Verdict:    VerdictHypothesis,
		SignalKind: SignalProfile,
		Evidence:   []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalProfile, Missing: "нет заявления"}},
	})
	evaluate(t, Assessment{
		Verdict:    VerdictUnsuitable,
		SignalKind: SignalProfile,
		Evidence:   []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalProfile, Mismatch: "не тот клиент"}},
	})

	cases := []struct {
		name       string
		assessment Assessment
	}{
		{name: "пустой вердикт", assessment: Assessment{SignalKind: SignalDirect}},
		{name: "неизвестный вердикт", assessment: Assessment{Verdict: "maybe", SignalKind: SignalDirect, Evidence: []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalDirect}}}},
		{name: "fits без свидетельств", assessment: Assessment{Verdict: VerdictFits, SignalKind: SignalDirect}},
		{name: "fits с профилем", assessment: Assessment{Verdict: VerdictFits, SignalKind: SignalProfile, Evidence: []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalProfile}}}},
		{name: "fits с неполным свидетельством", assessment: Assessment{Verdict: VerdictFits, SignalKind: SignalDirect, Evidence: []AssessmentEvidence{{Criterion: "c", SignalKind: SignalDirect}}}},
		{name: "hypothesis без missing", assessment: Assessment{Verdict: VerdictHypothesis, SignalKind: SignalProfile, Evidence: []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalProfile}}}},
		{name: "hypothesis с чужим сигналом", assessment: Assessment{Verdict: VerdictHypothesis, SignalKind: SignalDirect, Evidence: []AssessmentEvidence{{Criterion: "c", Fact: "f", SourceURL: "u", Reasoning: "r", SignalKind: SignalDirect, Missing: "m"}}}},
		{name: "unsuitable без mismatch", assessment: Assessment{Verdict: VerdictUnsuitable, SignalKind: SignalProfile}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.assessment.Validate(); err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.assessment)
			}
		})
	}
}

func TestPlanLimitsValidate(t *testing.T) {
	if err := (PlanLimits{MaxDepth: 2, MaxTimeSeconds: 120}).Validate(); err != nil {
		t.Fatalf("валидные лимиты отвергнуты: %v", err)
	}
	if err := (PlanLimits{}).Validate(); err != nil {
		t.Fatalf("пустые лимиты должны быть допустимы: %v", err)
	}
	if err := (PlanLimits{MaxDepth: -1}).Validate(); err == nil {
		t.Fatal("отрицательная глубина должна быть отвергнута")
	}
	if err := (PlanLimits{MaxTimeSeconds: -1}).Validate(); err == nil {
		t.Fatal("отрицательное время должно быть отвергнуто")
	}
}
