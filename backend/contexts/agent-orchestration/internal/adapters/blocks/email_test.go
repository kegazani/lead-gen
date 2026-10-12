package blocks_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/blocks"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/content-production/contentrequest"
)

// emailInput — вход шага производства черновика.
func emailInput() ports.StepInput {
	return ports.StepInput{
		RunID: "run-1", StepID: "generate-email", TenantID: "tenant-1",
		MissionID: "mission-1", CompanyID: "company-1", Criteria: "оффер: автоматизация продаж",
	}
}

func TestGenerateEmailExecutorSuccess(t *testing.T) {
	var got contentrequest.Intent
	contract := contentrequest.InProc(contentrequest.InProcHandler{
		RequestFunc: func(_ context.Context, in contentrequest.Intent) (contentrequest.Result, error) {
			got = in
			return contentrequest.Result{RequestID: in.RequestID, AssetID: "company-1", VersionID: "v3", State: "requested"}, nil
		},
	})
	result, err := blocks.NewGenerateEmailExecutor(contract, "").Execute(context.Background(), emailInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsSuccess() {
		t.Fatalf("исход = %q", result.Outcome.Kind())
	}
	if result.ArtifactRef != "company-1@v3" || string(result.DraftRef) != "company-1@v3" || result.DraftVersion != 3 {
		t.Fatalf("артефакт = %q черновик = %q v%d", result.ArtifactRef, result.DraftRef, result.DraftVersion)
	}
	// Ключ идемпотентности запроса детерминирован из запуска и шага.
	if got.RequestID != "run-1:generate-email" {
		t.Fatalf("RequestID = %q, ожидался ключ идемпотентности шага", got.RequestID)
	}
	if got.Language != "ru" || got.Body == "" || got.CorrelationID != "run-1" {
		t.Fatalf("запрос контракта = %+v", got)
	}
}

func TestGenerateEmailExecutorOwnerOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind domain.BlockOutcomeKind
	}{
		{"недоступность", contentrequest.ErrUnavailable, domain.OutcomeUnavailable},
		{"отказ владельца", contentrequest.ErrRejected, domain.OutcomeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract := contentrequest.InProc(contentrequest.InProcHandler{
				RequestFunc: func(context.Context, contentrequest.Intent) (contentrequest.Result, error) {
					return contentrequest.Result{}, tc.err
				},
			})
			result, err := blocks.NewGenerateEmailExecutor(contract, "").Execute(context.Background(), emailInput())
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Outcome.Kind() != tc.wantKind {
				t.Fatalf("исход = %q, ожидался %q", result.Outcome.Kind(), tc.wantKind)
			}
		})
	}
}

func TestGenerateEmailExecutorTransportErrorPropagates(t *testing.T) {
	transport := errors.New("сбой транспорта")
	contract := contentrequest.InProc(contentrequest.InProcHandler{
		RequestFunc: func(context.Context, contentrequest.Intent) (contentrequest.Result, error) {
			return contentrequest.Result{}, transport
		},
	})
	if _, err := blocks.NewGenerateEmailExecutor(contract, "").Execute(context.Background(), emailInput()); !errors.Is(err, transport) {
		t.Fatalf("ошибка = %v, ожидалась транспортная", err)
	}
}

func TestGenerateEmailExecutorWithoutContract(t *testing.T) {
	result, err := blocks.NewGenerateEmailExecutor(nil, "").Execute(context.Background(), emailInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}

func TestGenerateEmailExecutorWithoutOffer(t *testing.T) {
	contract := contentrequest.InProc(contentrequest.InProcHandler{
		RequestFunc: func(context.Context, contentrequest.Intent) (contentrequest.Result, error) {
			t.Fatal("контракт не должен вызываться без оффера")
			return contentrequest.Result{}, nil
		},
	})
	in := emailInput()
	in.Criteria = "  "
	result, err := blocks.NewGenerateEmailExecutor(contract, "").Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}

func TestGenerateEmailExecutorOwnerMissingVersion(t *testing.T) {
	contract := contentrequest.InProc(contentrequest.InProcHandler{
		RequestFunc: func(context.Context, contentrequest.Intent) (contentrequest.Result, error) {
			return contentrequest.Result{AssetID: "company-1"}, nil
		},
	})
	result, err := blocks.NewGenerateEmailExecutor(contract, "").Execute(context.Background(), emailInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}
