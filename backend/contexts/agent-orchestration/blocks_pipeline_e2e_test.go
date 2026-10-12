package agentorchestration

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/query"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// e2ePublisher — двойник издателя approved-action, фиксирующий публикации.
type e2ePublisher struct {
	published []approvedaction.ApprovedAction
}

func (p *e2ePublisher) Publish(_ context.Context, action approvedaction.ApprovedAction) error {
	p.published = append(p.published, action)
	return nil
}

// e2eApprovedSource — двойник источника утверждённого действия.
type e2eApprovedSource struct {
	got    ports.ApprovedActionLookup
	calls  int
	action approvedaction.ApprovedAction
}

func (s *e2eApprovedSource) ApprovedAction(_ context.Context, in ports.ApprovedActionLookup) (approvedaction.ApprovedAction, error) {
	s.calls++
	s.got = in
	return s.action, nil
}

// e2eRelevance — двойник read-контракта lead-relevance с вердиктом «подходит».
type e2eRelevance struct{ calls int }

func (r *e2eRelevance) Evaluate(_ context.Context, _ ports.RelevanceQuery) (ports.RelevanceEvaluation, error) {
	r.calls++
	return ports.RelevanceEvaluation{Verdict: ports.RelevanceSuitable, BasisRef: "basis-1"}, nil
}

// e2eApproval — двойник подачи черновика на утверждение.
type e2eApproval struct {
	calls int
	got   ports.ApprovalSubmission
}

func (a *e2eApproval) SubmitForApproval(_ context.Context, in ports.ApprovalSubmission) error {
	a.calls++
	a.got = in
	return nil
}

// TestPipelineReachesSendAfterApproval проверяет сквозной прогон компании по
// пайплайну из реализованных блоков: поиск → оценка релевантности → черновик →
// утверждение → отправка. Шаг отправки исполняется только после положительного
// решения оператора, привязанного к точной версии черновика (спека
// company-agent-orchestration, «Утверждение необратимых действий»).
func TestPipelineReachesSendAfterApproval(t *testing.T) {
	ctx := context.Background()
	registry, err := DefaultBlocks()
	if err != nil {
		t.Fatalf("DefaultBlocks: %v", err)
	}
	relevance := &e2eRelevance{}
	approval := &e2eApproval{}
	publisher := &e2ePublisher{}
	source := &e2eApprovedSource{action: approvedaction.ApprovedAction{ApprovedActionID: "act-1"}}

	cfg := testConfig()
	cfg.Blocks = registry
	cfg.LeadSearch = leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(context.Context, leadsearch.Query) (leadsearch.Result, error) {
			return leadsearch.Result{Found: 1, Leads: []leadsearch.LeadRef{{ID: "company-1", Origin: leadsearch.OriginPool}}}, nil
		},
	})
	cfg.LeadRelevance = relevance
	cfg.ContentRequest = contentrequest.InProc(contentrequest.InProcHandler{
		RequestFunc: func(_ context.Context, in contentrequest.Intent) (contentrequest.Result, error) {
			return contentrequest.Result{RequestID: in.RequestID, AssetID: in.AssetID, VersionID: "v1", State: "requested"}, nil
		},
	})
	cfg.ApprovalSubmitter = approval
	cfg.ApprovedAction = publisher
	cfg.ApprovedActionSource = source

	slice, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	steps := []command.PipelineStepInput{
		{ID: string(BlockSearch), BlockRef: string(BlockSearch)},
		{ID: string(BlockClassify), BlockRef: string(BlockClassify)},
		{ID: string(BlockGenerateEmail), BlockRef: string(BlockGenerateEmail)},
		{ID: string(BlockAwaitApproval), BlockRef: string(BlockAwaitApproval)},
		{ID: string(BlockSend), BlockRef: string(BlockSend)},
	}
	if _, err := slice.PublishPipeline.Handle(ctx, command.PublishPipeline{
		PipelineID: "pipe-1", TenantID: "tenant-1", Steps: steps,
	}); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}
	started, err := slice.StartRun.Handle(ctx, command.StartRun{
		TenantID: "tenant-1", MissionID: "mission-1", CompanyID: "company-1",
		PipelineID: "pipe-1", Criteria: "оффер: автоматизация продаж",
	})
	if err != nil {
		t.Fatalf("запуск агента: %v", err)
	}

	// Первые три шага проходят успешно.
	for _, want := range []domain.BlockRef{BlockSearch, BlockClassify, BlockGenerateEmail} {
		result := runStep(t, slice, started.RunID, "tenant-1")
		if !result.Outcome.IsSuccess() || result.BlockRef != string(want) {
			t.Fatalf("шаг %s: исход = %q", want, result.Outcome.Kind())
		}
	}

	// Шаг утверждения подаёт черновик и переводит запуск в ожидание решения.
	awaitResult := runStep(t, slice, started.RunID, "tenant-1")
	if awaitResult.BlockRef != string(BlockAwaitApproval) || !awaitResult.AwaitingApproval {
		t.Fatalf("шаг утверждения: блок = %q ожидание = %v", awaitResult.BlockRef, awaitResult.AwaitingApproval)
	}
	if awaitResult.RunStatus != domain.StatusAwaitingApproval || approval.calls != 1 {
		t.Fatalf("статус = %q подач = %d", awaitResult.RunStatus, approval.calls)
	}
	if approval.got.MaterialRef != "company-1@v1" || approval.got.DraftVersion != 1 {
		t.Fatalf("подача на утверждение = %+v", approval.got)
	}

	// Отправка без решения оператора невозможна.
	blocked := runStep(t, slice, started.RunID, "tenant-1")
	if blocked.RunStatus != domain.StatusAwaitingApproval {
		t.Fatalf("отправка обошла утверждение: статус = %q", blocked.RunStatus)
	}
	if len(publisher.published) != 0 {
		t.Fatal("письмо отправлено без решения оператора")
	}

	// Положительное решение по точной версии черновика возвращает запуск в работу.
	if err := slice.RecordApproval.Handle(ctx, command.RecordApproval{
		RunID: started.RunID, TenantID: "tenant-1", DraftRef: "company-1@v1", DraftVersion: 1,
		Approved: true, Actor: "operator-1",
	}); err != nil {
		t.Fatalf("решение оператора: %v", err)
	}

	// Шаг отправки публикует утверждённое действие и завершает запуск.
	sendResult := runStep(t, slice, started.RunID, "tenant-1")
	if sendResult.BlockRef != string(BlockSend) || !sendResult.Outcome.IsSuccess() {
		t.Fatalf("шаг отправки: блок = %q исход = %q", sendResult.BlockRef, sendResult.Outcome.Kind())
	}
	if sendResult.RunStatus != domain.StatusCompleted {
		t.Fatalf("статус после отправки = %q, ожидался %q", sendResult.RunStatus, domain.StatusCompleted)
	}
	if len(publisher.published) != 1 || source.calls != 1 {
		t.Fatalf("опубликовано = %d, источник опрошен = %d", len(publisher.published), source.calls)
	}

	state, err := slice.RunState.Handle(ctx, query.RunStateQuery{RunID: started.RunID, TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("состояние запуска: %v", err)
	}
	if state.Status != string(domain.StatusCompleted) || len(state.Steps) != len(steps) {
		t.Fatalf("наблюдаемое состояние = %+v", state)
	}
}

// runStep прогоняет текущий шаг запуска и возвращает наблюдаемый исход.
func runStep(t *testing.T, slice *Slice, runID, tenant string) command.RunStepResult {
	t.Helper()
	result, err := slice.RunStep.Handle(context.Background(), command.RunStep{RunID: runID, TenantID: tenant})
	if err != nil {
		t.Fatalf("прогон шага: %v", err)
	}
	return result
}
