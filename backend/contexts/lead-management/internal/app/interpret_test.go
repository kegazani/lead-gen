package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/llm"
)

// SetInterpretation дополняет test-дабл memJobs (fakes_test.go не трогаем):
// фиксирует интерпретацию задания отдельным полем, не меняя статус.
func (m *memJobs) SetInterpretation(_ context.Context, id string, interpretation *llm.Interpretation) error {
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	j.Interpretation = interpretation
	return nil
}

// interpretFakeLLM — локальный фейк llm.Port для тестов интерпретации.
// Встраивание llm.Port закрывает методы, которые в этих тестах не вызываются.
type interpretFakeLLM struct {
	llm.Port

	interpretation llm.Interpretation
	usage          llm.Usage
	err            error

	interpretCalls int
	planCalls      int
	planRequests   []llm.PlanRequest
}

// Убеждаемся, что фейк удовлетворяет порту.
var _ llm.Port = (*interpretFakeLLM)(nil)

func (f *interpretFakeLLM) Interpret(_ context.Context, _ llm.InterpretRequest) (llm.Interpretation, llm.Usage, error) {
	f.interpretCalls++
	if f.err != nil {
		return llm.Interpretation{}, llm.Usage{}, f.err
	}
	return f.interpretation, f.usage, nil
}

func (f *interpretFakeLLM) Plan(_ context.Context, req llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	f.planCalls++
	f.planRequests = append(f.planRequests, req)
	return []llm.Target{{Query: req.Query}}, llm.Usage{}, nil
}

// TestInterpretQuery_ReturnsInterpretationAndTokens проверяет, что use case
// отдаёт критерии из llm.Port.Interpret и расход токенов, не строя технический
// план: Plan не вызывается, целей и URL use case не формирует.
func TestInterpretQuery_ReturnsInterpretationAndTokens(t *testing.T) {
	want := llm.Interpretation{
		TargetLead:        "B2B-агентство",
		NeedSignals:       []string{"ищет подрядчика лидогенерации"},
		Exclusions:        []string{"B2C"},
		HypothesisSignals: []string{"работает с несколькими клиентами"},
	}
	f := &interpretFakeLLM{interpretation: want, usage: llm.Usage{TotalTokens: 42}}
	u := &InterpretQuery{LLM: f, Clock: fakeClock{t: testTime}}

	got, tokens, err := u.Interpret(context.Background(), "  агентства b2b  ")
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if tokens != 42 {
		t.Fatalf("токены = %d, ожидалось 42", tokens)
	}
	if got.TargetLead != want.TargetLead || len(got.NeedSignals) != 1 || got.NeedSignals[0] != want.NeedSignals[0] {
		t.Fatalf("интерпретация = %+v, ожидалась %+v", got, want)
	}
	if got.Uncertainty {
		t.Fatal("неопределённость не ожидалась")
	}
	if f.planCalls != 0 {
		t.Fatalf("Plan вызван %d раз, ожидалось 0: интерпретация не строит технический план", f.planCalls)
	}
}

// TestInterpretQuery_EmptyQuery проверяет отказ на пустой запрос.
func TestInterpretQuery_EmptyQuery(t *testing.T) {
	f := &interpretFakeLLM{}
	u := &InterpretQuery{LLM: f}

	for _, query := range []string{"", "   "} {
		if _, _, err := u.Interpret(context.Background(), query); !errors.Is(err, errEmptyQuery) {
			t.Fatalf("Interpret(%q): ошибка = %v, ожидалась errEmptyQuery", query, err)
		}
	}
	if f.interpretCalls != 0 {
		t.Fatalf("LLM вызван %d раз на пустой запрос, ожидалось 0", f.interpretCalls)
	}
}

// TestInterpretQuery_NoLLM проверяет явную неопределённость без выдуманных
// критериев, когда LLM не настроен.
func TestInterpretQuery_NoLLM(t *testing.T) {
	u := &InterpretQuery{Clock: fakeClock{t: testTime}}

	got, tokens, err := u.Interpret(context.Background(), "агентства b2b")
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if !got.Uncertainty {
		t.Fatal("ожидалась явная неопределённость без LLM")
	}
	if got.TargetLead != "" || len(got.NeedSignals) != 0 || len(got.Exclusions) != 0 || len(got.HypothesisSignals) != 0 {
		t.Fatalf("критерии не должны выдумываться без LLM: %+v", got)
	}
	if tokens != 0 {
		t.Fatalf("токены = %d, ожидалось 0", tokens)
	}
}

// TestInterpretQuery_LLMErrorPropagates проверяет, что ошибка LLM пробрасывается.
func TestInterpretQuery_LLMErrorPropagates(t *testing.T) {
	sentinel := errors.New("провайдер LLM недоступен")
	f := &interpretFakeLLM{err: sentinel}
	u := &InterpretQuery{LLM: f}

	if _, _, err := u.Interpret(context.Background(), "агентства b2b"); !errors.Is(err, sentinel) {
		t.Fatalf("ошибка = %v, ожидалась %v", err, sentinel)
	}
}

// TestEnqueueInterpreted_KeepsInterpretationAndPlanApart проверяет, что
// постановка сохраняет интерпретацию отдельно от технического плана обхода.
func TestEnqueueInterpreted_KeepsInterpretationAndPlanApart(t *testing.T) {
	f := &interpretFakeLLM{
		interpretation: llm.Interpretation{
			TargetLead:  "B2B-агентство",
			NeedSignals: []string{"ищет лидогенерацию"},
		},
		usage: llm.Usage{TotalTokens: 11},
	}
	store := newMemJobs()
	r := &RunSearch{Jobs: store, LLM: f, Clock: fakeClock{t: testTime}}

	job, err := r.EnqueueInterpreted(context.Background(), "агентства b2b", nil, ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueInterpreted: %v", err)
	}
	if job.Interpretation == nil || job.Interpretation.TargetLead != "B2B-агентство" {
		t.Fatalf("задание без интерпретации: %+v", job.Interpretation)
	}
	if f.planCalls != 1 {
		t.Fatalf("Plan вызван %d раз, ожидался 1 (технический план)", f.planCalls)
	}
	// Интерпретация задания доходит до планирования: генерация плана опирается
	// на критерии, а не только на текст запроса.
	if len(f.planRequests) != 1 || f.planRequests[0].Interpretation.TargetLead != "B2B-агентство" {
		t.Fatalf("интерпретация не дошла до Plan: %+v", f.planRequests)
	}

	// План и интерпретация — разные поля: план даёт поисковый запрос, а
	// интерпретация — критерии релевантности.
	payload, err := ports.DecodeSearchJobPayload(store.payload[job.ID])
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if payload.Plan == nil || len(payload.Plan.Targets) != 1 || payload.Plan.Targets[0].Query != "агентства b2b" {
		t.Fatalf("технический план не сохранён: %+v", payload.Plan)
	}
	if stored := store.byID[job.ID].Interpretation; stored == nil || stored.TargetLead != "B2B-агентство" {
		t.Fatalf("интерпретация не сохранена в хранилище: %+v", stored)
	}
}

// TestEnqueueInterpreted_InterpretErrorYieldsUncertainty проверяет, что ошибка
// интерпретации не выдумывает критерии и не теряет задание: оно ставится с явной
// неопределённостью, причиной сбоя и записью в лог.
func TestEnqueueInterpreted_InterpretErrorYieldsUncertainty(t *testing.T) {
	f := &interpretFakeLLM{err: errors.New("LLM недоступен")}
	store := newMemJobs()
	var buf bytes.Buffer
	r := &RunSearch{Jobs: store, LLM: f, Clock: fakeClock{t: testTime}, Logger: observ.NewLoggerWithWriter(&buf)}

	job, err := r.EnqueueInterpreted(context.Background(), "агентства b2b", nil, ports.PlanLimits{MaxDepth: 1}, 0, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueInterpreted: %v", err)
	}
	if job.Interpretation == nil || !job.Interpretation.Uncertainty {
		t.Fatalf("ожидалась явная неопределённость: %+v", job.Interpretation)
	}
	if job.Interpretation.TargetLead != "" || len(job.Interpretation.NeedSignals) != 0 {
		t.Fatalf("критерии не должны выдумываться: %+v", job.Interpretation)
	}
	if job.Interpretation.Reason == "" {
		t.Fatalf("причина сбоя не сохранена в интерпретации: %+v", job.Interpretation)
	}
	if log := buf.String(); !strings.Contains(log, "LLM недоступен") || !strings.Contains(log, "интерпретация запроса не удалась") {
		t.Fatalf("лог не содержит причину сбоя интерпретации: %s", log)
	}
}

// TestEnqueueInterpreted_Validation проверяет отказы на пустой запрос и
// отрицательный бюджет.
func TestEnqueueInterpreted_Validation(t *testing.T) {
	r := &RunSearch{Jobs: newMemJobs(), LLM: &interpretFakeLLM{}}

	if _, err := r.EnqueueInterpreted(context.Background(), "  ", nil, ports.PlanLimits{}, 0, 0, 0); !errors.Is(err, errEmptyQuery) {
		t.Fatalf("пустой запрос: ошибка = %v, ожидалась errEmptyQuery", err)
	}
	if _, err := r.EnqueueInterpreted(context.Background(), "агентства b2b", nil, ports.PlanLimits{}, -5, 0, 0); err == nil {
		t.Fatal("отрицательный бюджет должен быть отклонён")
	}
}
