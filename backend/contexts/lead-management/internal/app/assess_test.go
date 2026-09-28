package app

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"lead-gen/contracts/integration-platform/llm"
)

// assessFakeLLM — локальный фейк llm.Port для тестов оценки. Отдаёт заранее
// заданный llm.Assessment, запоминает запрос и позволяет вернуть ошибку.
type assessFakeLLM struct {
	assessment llm.Assessment
	usage      llm.Usage
	err        error
	requests   []llm.AssessRequest
}

func (f *assessFakeLLM) Plan(context.Context, llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	return nil, llm.Usage{}, nil
}

func (f *assessFakeLLM) Suggest(context.Context, llm.SuggestRequest) ([]llm.Suggestion, llm.Usage, error) {
	return nil, llm.Usage{}, nil
}

func (f *assessFakeLLM) Converse(context.Context, llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	return llm.ConverseReply{}, llm.Usage{}, nil
}

func (f *assessFakeLLM) ConverseStream(context.Context, llm.ConverseRequest) iter.Seq2[llm.ConverseDelta, error] {
	return func(yield func(llm.ConverseDelta, error) bool) {}
}

func (f *assessFakeLLM) Interpret(context.Context, llm.InterpretRequest) (llm.Interpretation, llm.Usage, error) {
	return llm.Interpretation{}, llm.Usage{}, nil
}

func (f *assessFakeLLM) Assess(_ context.Context, req llm.AssessRequest) (llm.Assessment, llm.Usage, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return llm.Assessment{}, f.usage, f.err
	}
	return f.assessment, f.usage, nil
}

// assessInput — базовый вход оценки для тестов.
func assessInput() AssessInput {
	return AssessInput{
		JobID:              "job-1",
		CompanyID:          "company-1",
		CompanyName:        "ООО Ромашка",
		CompanyDescription: "Производство упаковки",
		Interpretation: llm.Interpretation{
			TargetLead:  "производитель упаковки",
			NeedSignals: []string{"ищет подрядчика по логистике"},
		},
		Material: []llm.AssessMaterial{{URL: "https://example.com/news", Text: "текст"}},
	}
}

func newAssessCompany(f *assessFakeLLM) *AssessCompany {
	return &AssessCompany{LLM: f, Clock: fakeClock{t: testTime}}
}

func TestAssessCompany_DirectStatement(t *testing.T) {
	f := &assessFakeLLM{assessment: llm.Assessment{
		Verdict:    llm.VerdictFits,
		SignalKind: llm.SignalDirect,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "ищет подрядчика по логистике",
			Fact:       "компания объявила тендер на логистику",
			SourceURL:  "https://example.com/tender",
			Reasoning:  "объявление прямо называет потребность",
			SignalKind: llm.SignalDirect,
		}},
	}}

	got, _, err := newAssessCompany(f).Assess(context.Background(), assessInput())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict() != "fits" {
		t.Fatalf("вердикт = %q, ожидался fits", got.Verdict())
	}
	if got.SignalKind() != "direct" {
		t.Fatalf("тип сигнала = %q, ожидался direct", got.SignalKind())
	}
	ev := got.Evidence()
	if len(ev) != 1 || ev[0].SourceURL == "" {
		t.Fatalf("свидетельство без источника: %+v", ev)
	}
	if ev[0].Fact != "компания объявила тендер на логистику" {
		t.Fatalf("факт изменён: %q", ev[0].Fact)
	}
}

func TestAssessCompany_IndirectSignal(t *testing.T) {
	reasoning := "рост вакансий логиста указывает на перестройку логистики"
	f := &assessFakeLLM{assessment: llm.Assessment{
		Verdict:    llm.VerdictFits,
		SignalKind: llm.SignalIndirect,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "ищет подрядчика по логистике",
			Fact:       "открыто пять вакансий логиста",
			SourceURL:  "https://example.com/jobs",
			Reasoning:  reasoning,
			SignalKind: llm.SignalIndirect,
		}},
	}}

	got, _, err := newAssessCompany(f).Assess(context.Background(), assessInput())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.SignalKind() != "indirect" {
		t.Fatalf("тип сигнала = %q, ожидался indirect", got.SignalKind())
	}
	ev := got.Evidence()
	if ev[0].Reasoning != reasoning {
		t.Fatalf("обоснование связи потеряно: %q", ev[0].Reasoning)
	}
	// Косвенный сигнал не утверждает готовность купить: обоснование описывает
	// связь факта и потребности, а не намерение.
	if strings.Contains(strings.ToLower(ev[0].Reasoning), "готов") {
		t.Fatalf("косвенный сигнал утверждает готовность: %q", ev[0].Reasoning)
	}
}

func TestAssessCompany_ProfileOnlyHypothesis(t *testing.T) {
	f := &assessFakeLLM{assessment: llm.Assessment{
		Verdict:    llm.VerdictHypothesis,
		SignalKind: llm.SignalProfile,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "ищет подрядчика по логистике",
			Fact:       "компания производит упаковку",
			SourceURL:  "https://example.com/about",
			Reasoning:  "отрасль совпадает с профилем целевого лида",
			SignalKind: llm.SignalProfile,
			Missing:    "нет наблюдаемого признака поиска подрядчика",
		}},
	}}

	got, _, err := newAssessCompany(f).Assess(context.Background(), assessInput())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict() != "hypothesis" {
		t.Fatalf("вердикт = %q, ожидался hypothesis", got.Verdict())
	}
	if got.SignalKind() != "profile" {
		t.Fatalf("тип сигнала = %q, ожидался profile", got.SignalKind())
	}
	ev := got.Evidence()
	if len(ev) == 0 || strings.TrimSpace(ev[0].Missing) == "" {
		t.Fatalf("гипотеза без недостающего свидетельства: %+v", ev)
	}
}

func TestAssessCompany_UnsuitableMismatch(t *testing.T) {
	f := &assessFakeLLM{assessment: llm.Assessment{
		Verdict:    llm.VerdictUnsuitable,
		SignalKind: llm.SignalProfile,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  "ищет подрядчика по логистике",
			Fact:       "компания закрыла логистическое подразделение",
			SourceURL:  "https://example.com/news",
			Reasoning:  "потребность отсутствует",
			SignalKind: llm.SignalProfile,
			Mismatch:   "исключение: компания прекратила деятельность",
		}},
	}}

	got, _, err := newAssessCompany(f).Assess(context.Background(), assessInput())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict() != "unsuitable" {
		t.Fatalf("вердикт = %q, ожидался unsuitable", got.Verdict())
	}
	ev := got.Evidence()
	if len(ev) == 0 || strings.TrimSpace(ev[0].Mismatch) == "" {
		t.Fatalf("«Не подходит» без несоответствия: %+v", ev)
	}
}

func TestAssessCompany_NoLLM(t *testing.T) {
	u := &AssessCompany{Clock: fakeClock{t: testTime}}
	_, _, err := u.Assess(context.Background(), assessInput())
	if !errors.Is(err, errAssessUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась errAssessUnavailable", err)
	}
}

func TestAssessCompany_LLMErrorPropagates(t *testing.T) {
	sentinel := errors.New("llm: провайдер недоступен")
	f := &assessFakeLLM{err: sentinel}
	_, _, err := newAssessCompany(f).Assess(context.Background(), assessInput())
	if !errors.Is(err, sentinel) {
		t.Fatalf("ошибка = %v, ожидалась %v", err, sentinel)
	}
}

func TestAssessCompany_EmptyIDs(t *testing.T) {
	f := &assessFakeLLM{}
	u := newAssessCompany(f)

	in := assessInput()
	in.JobID = " "
	if _, _, err := u.Assess(context.Background(), in); !errors.Is(err, errEmptyAssessJobID) {
		t.Fatalf("пустой job_id: ошибка = %v", err)
	}

	in = assessInput()
	in.CompanyID = ""
	if _, _, err := u.Assess(context.Background(), in); !errors.Is(err, errEmptyAssessCompanyID) {
		t.Fatalf("пустой company_id: ошибка = %v", err)
	}

	if len(f.requests) != 0 {
		t.Fatalf("порт вызван при невалидном входе: %d вызовов", len(f.requests))
	}
}

func TestAssessCompany_TokensAndMapping(t *testing.T) {
	f := &assessFakeLLM{
		usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		assessment: llm.Assessment{
			Verdict:    llm.VerdictFits,
			SignalKind: llm.SignalDirect,
			Evidence: []llm.AssessmentEvidence{{
				Criterion:  "критерий",
				Fact:       "факт",
				SourceURL:  "https://example.com",
				Reasoning:  "обоснование",
				SignalKind: llm.SignalDirect,
			}},
		},
	}

	got, tokens, err := newAssessCompany(f).Assess(context.Background(), assessInput())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if tokens != 15 {
		t.Fatalf("токены = %d, ожидалось 15", tokens)
	}
	if got.JobID() != "job-1" || got.CompanyID() != "company-1" {
		t.Fatalf("ключ (job, company) = (%q, %q)", got.JobID(), got.CompanyID())
	}
	if !got.CreatedAt().Equal(testTime) {
		t.Fatalf("момент оценки = %v, ожидался %v", got.CreatedAt(), testTime)
	}
	// Запрос к порту должен нести интерпретацию и материал без потерь.
	if len(f.requests) != 1 {
		t.Fatalf("вызовов порта = %d, ожидался 1", len(f.requests))
	}
	req := f.requests[0]
	if req.JobID != "job-1" || req.CompanyName != "ООО Ромашка" {
		t.Fatalf("запрос искажён: %+v", req)
	}
	if len(req.Interpretation.NeedSignals) != 1 || len(req.Material) != 1 {
		t.Fatalf("интерпретация/материал потеряны: %+v", req)
	}
}

func TestAssessCompany_DomainInvariantRejectsBadResponse(t *testing.T) {
	// Модель вернула «Подходит» без свидетельства с полями — домен отвергает,
	// вердикт не подменяется.
	f := &assessFakeLLM{assessment: llm.Assessment{
		Verdict:    llm.VerdictFits,
		SignalKind: llm.SignalDirect,
	}}
	_, _, err := newAssessCompany(f).Assess(context.Background(), assessInput())
	if err == nil {
		t.Fatal("ожидалась ошибка инварианта домена")
	}
}
