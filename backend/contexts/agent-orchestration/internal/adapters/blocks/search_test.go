package blocks_test

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/blocks"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/lead-management/leadsearch"
)

// stepInput — вход шага поиска с заданием и тенантом.
func stepInput(criteria string) ports.StepInput {
	return ports.StepInput{MissionID: "mission-1", TenantID: "tenant-1", Criteria: criteria}
}

func TestSearchExecutorSuccess(t *testing.T) {
	contract := leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(context.Context, leadsearch.Query) (leadsearch.Result, error) {
			return leadsearch.Result{Found: 2, Leads: []leadsearch.LeadRef{{ID: "company-9", Origin: leadsearch.OriginPool}}}, nil
		},
	})
	executor := blocks.NewSearchExecutor(contract, 2)

	result, err := executor.Execute(context.Background(), stepInput("ищем IT-компании"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsSuccess() || result.ArtifactRef != "company-9" {
		t.Fatalf("исход = %q артефакт = %q", result.Outcome.Kind(), result.ArtifactRef)
	}
}

func TestSearchExecutorOwnerUnavailable(t *testing.T) {
	contract := leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(context.Context, leadsearch.Query) (leadsearch.Result, error) {
			return leadsearch.Result{}, leadsearch.ErrUnavailable
		},
	})
	result, err := blocks.NewSearchExecutor(contract, 1).Execute(context.Background(), stepInput("цель"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}

func TestSearchExecutorOwnerRejected(t *testing.T) {
	contract := leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(context.Context, leadsearch.Query) (leadsearch.Result, error) {
			return leadsearch.Result{}, leadsearch.ErrRejected
		},
	})
	result, err := blocks.NewSearchExecutor(contract, 1).Execute(context.Background(), stepInput("цель"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Outcome.Kind() != domain.OutcomeFailed {
		t.Fatalf("исход = %q, ожидался отказ", result.Outcome.Kind())
	}
}

func TestSearchExecutorWithoutContract(t *testing.T) {
	result, err := blocks.NewSearchExecutor(nil, 1).Execute(context.Background(), stepInput("цель"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}

func TestSearchExecutorWithoutCriteria(t *testing.T) {
	contract := leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(context.Context, leadsearch.Query) (leadsearch.Result, error) {
			t.Fatal("контракт не должен вызываться без критерия")
			return leadsearch.Result{}, nil
		},
	})
	result, err := blocks.NewSearchExecutor(contract, 1).Execute(context.Background(), stepInput("  "))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}
