package agentorchestration

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/query"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
	"lead-gen/contracts/lead-management/leadsearch"
)

// TestDefaultBlocksRegistersFirstSet проверяет состав объявленных блоков первого
// набора и их признаки (design.md, Реш. 3).
func TestDefaultBlocksRegistersFirstSet(t *testing.T) {
	registry, err := DefaultBlocks()
	if err != nil {
		t.Fatalf("DefaultBlocks: %v", err)
	}
	want := []BlockRef{
		BlockSearch, BlockClassify, BlockEnrich, BlockVerifyEmail, BlockSizeAssess,
		BlockGenerateEmail, BlockAwaitApproval, BlockSend, BlockObserve, BlockDecideNext,
	}
	for _, ref := range want {
		if !registry.Registered(ref) {
			t.Fatalf("блок %q не зарегистрирован", ref)
		}
	}
	if len(registry.Blocks()) != len(want) {
		t.Fatalf("число блоков = %d, ожидалось %d", len(registry.Blocks()), len(want))
	}
	search, _ := registry.Lookup(BlockSearch)
	if !search.Mandatory() || search.Irreversible() || search.Contract() != "lead-search" {
		t.Fatalf("блок поиска = %+v", search)
	}
	send, _ := registry.Lookup(BlockSend)
	if !send.Irreversible() || !send.Mandatory() || send.Contract() != "delivery-command" {
		t.Fatalf("блок отправки = %+v", send)
	}
}

// TestNewBlockRegistryRejectsDuplicate проверяет, что расширение реестра отвергает
// дублирующуюся ссылку блока (две трактовки одного блока недопустимы).
func TestNewBlockRegistryRejectsDuplicate(t *testing.T) {
	block, err := NewBlock("search", "search", "lead-search", false, true)
	if err != nil {
		t.Fatalf("NewBlock: %v", err)
	}
	if _, err := NewBlockRegistry(block, block); !errors.Is(err, domain.ErrDuplicateBlock) {
		t.Fatalf("ошибка = %v, ожидалась ErrDuplicateBlock", err)
	}
}

// TestDefaultPipelineStepsBuildable проверяет, что первый набор шагов ссылается
// только на объявленные блоки и из него собирается публикуемая версия.
func TestDefaultPipelineStepsBuildable(t *testing.T) {
	registry, err := DefaultBlocks()
	if err != nil {
		t.Fatalf("DefaultBlocks: %v", err)
	}
	config, err := domain.NewStepConfig(nil, 0)
	if err != nil {
		t.Fatalf("NewStepConfig: %v", err)
	}
	specs := make([]domain.StepSpec, 0)
	for _, step := range DefaultPipelineSteps() {
		specs = append(specs, domain.StepSpec{ID: domain.PipelineStepID(step.ID), Ref: domain.BlockRef(step.BlockRef), Config: config})
	}
	pipeline, err := domain.NewPipeline("pipe-1", "tenant-1", 1, specs, registry)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if len(pipeline.Steps()) != len(DefaultPipelineSteps()) {
		t.Fatalf("число шагов = %d", len(pipeline.Steps()))
	}
}

// cmdSteps переводит публичные шаги в форму команды публикации.
func cmdSteps(steps []PipelineStep) []command.PipelineStepInput {
	out := make([]command.PipelineStepInput, 0, len(steps))
	for _, step := range steps {
		out = append(out, command.PipelineStepInput{ID: step.ID, BlockRef: step.BlockRef, Params: step.Params, MaxAttempts: step.MaxAttempts})
	}
	return out
}

// buildWithDefaultBlocks собирает срез контекста с реестром первого набора.
func buildWithDefaultBlocks(t *testing.T) *Slice {
	t.Helper()
	registry, err := DefaultBlocks()
	if err != nil {
		t.Fatalf("DefaultBlocks: %v", err)
	}
	cfg := testConfig()
	cfg.Blocks = registry
	slice, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return slice
}

// TestDefaultBlockSetRunsHonestUnavailable проверяет: публикация версии из первого
// набора блоков доступна, а блок без подключённого контракта-владельца честно
// недоступен (не подменяется успехом) и не закрывает запуск (AGENTS.md §3).
func TestDefaultBlockSetRunsHonestUnavailable(t *testing.T) {
	ctx := context.Background()
	slice := buildWithDefaultBlocks(t)

	if _, err := slice.PublishPipeline.Handle(ctx, command.PublishPipeline{
		PipelineID: "pipe-1", TenantID: "tenant-1", Steps: cmdSteps(DefaultPipelineSteps()),
	}); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}
	started, err := slice.StartRun.Handle(ctx, command.StartRun{
		TenantID: "tenant-1", MissionID: "mission-1", CompanyID: "company-1", PipelineID: "pipe-1",
	})
	if err != nil {
		t.Fatalf("запуск агента: %v", err)
	}

	// Первый блок — search; контракт lead-search не подключён → недоступность.
	result, err := slice.RunStep.Handle(ctx, command.RunStep{RunID: started.RunID, TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("прогон шага: %v", err)
	}
	if !result.Outcome.IsUnavailable() || result.RunStatus.IsTerminal() {
		t.Fatalf("исход = %q статус = %q: недоступность не должна закрывать запуск", result.Outcome.Kind(), result.RunStatus)
	}

	// Read-поверхность отдаёт ленту и состояние без подмены.
	summaries, err := slice.ListRuns.Handle(ctx, query.ListRunsQuery{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("лента: %v", err)
	}
	if len(summaries) != 1 || summaries[0].RunID != started.RunID {
		t.Fatalf("лента = %+v", summaries)
	}
	if summaries[0].Status != string(result.RunStatus) || summaries[0].CurrentBlock != string(BlockSearch) {
		t.Fatalf("сводка = %+v", summaries[0])
	}
	state, err := slice.RunState.Handle(ctx, query.RunStateQuery{RunID: started.RunID, TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("состояние: %v", err)
	}
	if len(state.Steps) != len(DefaultPipelineSteps()) || state.Steps[0].Outcome != string(domain.OutcomeUnavailable) {
		t.Fatalf("состояние шагов = %+v", state.Steps)
	}
}

// TestPipelineWithUnavailableOwnerBlockKeepsRunOpen проверяет блок, зависящий от
// отсутствующего контракта владельца (верификация адреса): шаг недоступен, запуск
// остаётся незавершённым.
func TestPipelineWithUnavailableOwnerBlockKeepsRunOpen(t *testing.T) {
	ctx := context.Background()
	slice := buildWithDefaultBlocks(t)

	steps := []command.PipelineStepInput{{ID: string(BlockVerifyEmail), BlockRef: string(BlockVerifyEmail)}}
	if _, err := slice.PublishPipeline.Handle(ctx, command.PublishPipeline{PipelineID: "pipe-verify", TenantID: "tenant-1", Steps: steps}); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}
	started, err := slice.StartRun.Handle(ctx, command.StartRun{
		TenantID: "tenant-1", MissionID: "mission-1", CompanyID: "company-1", PipelineID: "pipe-verify",
	})
	if err != nil {
		t.Fatalf("запуск агента: %v", err)
	}
	result, err := slice.RunStep.Handle(ctx, command.RunStep{RunID: started.RunID, TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("прогон шага: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
	if result.Outcome.Reason() == "" {
		t.Fatal("недоступность без нормализованной причины")
	}
	if result.RunStatus.IsTerminal() {
		t.Fatalf("недоступный шаг закрыл запуск: %q", result.RunStatus)
	}
}

// TestServiceReadModelAndPublishSurface проверяет публичную поверхность корневого
// пакета: публикацию версии и read-модель через собранный сервис.
func TestServiceReadModelAndPublishSurface(t *testing.T) {
	ctx := context.Background()
	registry, err := DefaultBlocks()
	if err != nil {
		t.Fatalf("DefaultBlocks: %v", err)
	}
	cfg := testConfig()
	cfg.Blocks = registry
	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	published, err := service.PublishPipeline(ctx, PublishPipelineRequest{
		PipelineID: "pipe-1", TenantID: "tenant-1", Steps: DefaultPipelineSteps(),
	})
	if err != nil {
		t.Fatalf("PublishPipeline: %v", err)
	}
	if published.Version != 1 {
		t.Fatalf("версия = %d, ожидалась 1", published.Version)
	}

	model := service.RunReadModel()
	summaries, err := model.List(ctx, agentrunstate.ListRequest{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("RunReadModel.List: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("лента пустого тенанта = %+v", summaries)
	}
	if _, err := model.List(ctx, agentrunstate.ListRequest{}); !errors.Is(err, ports.ErrMissingTenant) {
		t.Fatalf("выборка без тенанта = %v, ожидалась ErrMissingTenant", err)
	}
	if _, err := model.RunState(ctx, agentrunstate.Request{RunID: "run-missing", TenantID: "tenant-1"}); !errors.Is(err, agentrunstate.ErrNotFound) {
		t.Fatalf("RunState отсутствующего запуска = %v, ожидалась ErrNotFound", err)
	}
}

// TestServiceLeadSearchExecutorWiresSearch проверяет, что подключённый контракт
// lead-search делает блок поиска исполнимым: шаг завершается успехом.
func TestServiceLeadSearchExecutorWiresSearch(t *testing.T) {
	ctx := context.Background()
	registry, err := DefaultBlocks()
	if err != nil {
		t.Fatalf("DefaultBlocks: %v", err)
	}
	cfg := testConfig()
	cfg.Blocks = registry
	cfg.LeadSearch = leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(context.Context, leadsearch.Query) (leadsearch.Result, error) {
			return leadsearch.Result{Found: 1, Leads: []leadsearch.LeadRef{{ID: "company-7", Origin: leadsearch.OriginPool}}}, nil
		},
	})
	slice, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := slice.PublishPipeline.Handle(ctx, command.PublishPipeline{
		PipelineID: "pipe-1", TenantID: "tenant-1",
		Steps: []command.PipelineStepInput{{ID: string(BlockSearch), BlockRef: string(BlockSearch)}},
	}); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}
	started, err := slice.StartRun.Handle(ctx, command.StartRun{
		TenantID: "tenant-1", MissionID: "mission-1", CompanyID: "company-1", PipelineID: "pipe-1",
		Criteria: "ищем IT-компании",
	})
	if err != nil {
		t.Fatalf("запуск агента: %v", err)
	}
	result, err := slice.RunStep.Handle(ctx, command.RunStep{RunID: started.RunID, TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("прогон шага: %v", err)
	}
	if !result.Outcome.IsSuccess() || result.RunStatus != domain.StatusCompleted {
		t.Fatalf("исход = %q статус = %q, ожидался успех и завершение", result.Outcome.Kind(), result.RunStatus)
	}
}
