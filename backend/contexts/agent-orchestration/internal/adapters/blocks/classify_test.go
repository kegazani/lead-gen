package blocks_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/blocks"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// relevanceStub — тестовый двойник read-контракта lead-relevance.
type relevanceStub struct {
	evaluation ports.RelevanceEvaluation
	err        error
	gotQuery   ports.RelevanceQuery
	calls      int
}

func (s *relevanceStub) Evaluate(_ context.Context, query ports.RelevanceQuery) (ports.RelevanceEvaluation, error) {
	s.calls++
	s.gotQuery = query
	return s.evaluation, s.err
}

// classifyInput — вход шага оценки релевантности.
func classifyInput() ports.StepInput {
	return ports.StepInput{
		RunID: "run-1", StepID: "classify", TenantID: "tenant-1",
		MissionID: "mission-1", CompanyID: "company-1", Criteria: "ищем IT-компании",
	}
}

func TestClassifyExecutorSuitable(t *testing.T) {
	contract := &relevanceStub{evaluation: ports.RelevanceEvaluation{Verdict: ports.RelevanceSuitable}}
	result, err := blocks.NewClassifyExecutor(contract).Execute(context.Background(), classifyInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsSuccess() || result.ArtifactRef != "company-1" {
		t.Fatalf("исход = %q артефакт = %q", result.Outcome.Kind(), result.ArtifactRef)
	}
	if contract.gotQuery.MissionID != "mission-1" || contract.gotQuery.CompanyID != "company-1" {
		t.Fatalf("запрос оценки = %+v", contract.gotQuery)
	}
}

func TestClassifyExecutorUnsuitableFails(t *testing.T) {
	contract := &relevanceStub{evaluation: ports.RelevanceEvaluation{Verdict: ports.RelevanceUnsuitable}}
	result, err := blocks.NewClassifyExecutor(contract).Execute(context.Background(), classifyInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Outcome.Kind() != domain.OutcomeFailed || result.Outcome.Reason() == "" {
		t.Fatalf("исход = %q (%q), ожидался отказ с причиной", result.Outcome.Kind(), result.Outcome.Reason())
	}
}

func TestClassifyExecutorOwnerOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind domain.BlockOutcomeKind
	}{
		{"недоступность", ports.ErrRelevanceUnavailable, domain.OutcomeUnavailable},
		{"отказ владельца", ports.ErrRelevanceRejected, domain.OutcomeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := blocks.NewClassifyExecutor(&relevanceStub{err: tc.err}).Execute(context.Background(), classifyInput())
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Outcome.Kind() != tc.wantKind {
				t.Fatalf("исход = %q, ожидался %q", result.Outcome.Kind(), tc.wantKind)
			}
		})
	}
}

func TestClassifyExecutorTransportErrorPropagates(t *testing.T) {
	transport := errors.New("сбой транспорта")
	if _, err := blocks.NewClassifyExecutor(&relevanceStub{err: transport}).Execute(context.Background(), classifyInput()); !errors.Is(err, transport) {
		t.Fatalf("ошибка = %v, ожидалась транспортная", err)
	}
}

func TestClassifyExecutorWithoutContract(t *testing.T) {
	result, err := blocks.NewClassifyExecutor(nil).Execute(context.Background(), classifyInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}

func TestClassifyExecutorWithoutCompany(t *testing.T) {
	contract := &relevanceStub{evaluation: ports.RelevanceEvaluation{Verdict: ports.RelevanceSuitable}}
	in := classifyInput()
	in.CompanyID = " "
	result, err := blocks.NewClassifyExecutor(contract).Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() || contract.calls != 0 {
		t.Fatalf("исход = %q, вызовов контракта = %d", result.Outcome.Kind(), contract.calls)
	}
}

func TestClassifyExecutorUnknownVerdictUnavailable(t *testing.T) {
	contract := &relevanceStub{evaluation: ports.RelevanceEvaluation{Verdict: "нечто"}}
	result, err := blocks.NewClassifyExecutor(contract).Execute(context.Background(), classifyInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}
