package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	analytics "lead-gen/backend/contexts/analytics"
	platformgovernance "lead-gen/backend/contexts/platform-governance"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

// agentOrchestrationSelected — выбор сервиса agent-orchestration для тестов
// проводки источника операций.
func agentOrchestrationSelected() selectedService {
	return selectedService{Process: "agents", Mechanism: "in-process", ID: agentorchestration.ServiceID}
}

// supportServiceBuildOnce собирает supporting-сервис так же, как основной цикл, и
// переиспользует его как co-located владельца в тестах проводки источника
// операций.
func supportServiceBuildOnce(t *testing.T, opts buildOptions) func(selectedService) (composition.Service, error) {
	t.Helper()
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())
	return func(sel selectedService) (composition.Service, error) {
		return buildService(sel.ID, sel.Mechanism, opts, broker, stub)
	}
}

// TestBuildAgentOrchestrationConfiguresBlockRegistry проверяет, что сборка задаёт
// реестр объявленных блоков первого набора: публикация версии пайплайна поверх
// него проходит, а без реестра отвергалась бы неизвестным блоком.
func TestBuildAgentOrchestrationConfiguresBlockRegistry(t *testing.T) {
	opts := buildOptions{
		apiAddr:                ":0",
		agentWorkflowTaskQueue: agentorchestration.ServiceID,
		logger:                 observ.NewLogger(),
		metrics:                observ.NewMetrics(),
	}
	service, err := supportServiceBuildOnce(t, opts)(agentOrchestrationSelected())
	if err != nil {
		t.Fatalf("сборка сервиса agent-orchestration: %v", err)
	}
	owner, ok := service.(*agentorchestration.Service)
	if !ok {
		t.Fatalf("ожидался *agentorchestration.Service, got %T", service)
	}
	result, err := owner.PublishPipeline(context.Background(), agentorchestration.PublishPipelineRequest{
		PipelineID: "probe-pipeline",
		TenantID:   "tenant-1",
		Steps:      agentorchestration.DefaultPipelineSteps(),
	})
	if err != nil {
		t.Fatalf("публикация поверх реестра блоков: %v", err)
	}
	if result.Version != 1 {
		t.Fatalf("версия = %d, ожидалась 1", result.Version)
	}
}

// TestBuildAgentOrchestrationBootstrapPipeline проверяет разовую публикацию
// первого пайплайна при включённом флаге: сборка не падает и публикует для
// заданных тенантов расписания.
func TestBuildAgentOrchestrationBootstrapPipeline(t *testing.T) {
	opts := buildOptions{
		apiAddr:                ":0",
		agentWorkflowTaskQueue: agentorchestration.ServiceID,
		agentBootstrapPipeline: true,
		agentScheduleTenants:   []string{"tenant-1", "tenant-1"},
		logger:                 observ.NewLogger(),
		metrics:                observ.NewMetrics(),
	}
	service, err := supportServiceBuildOnce(t, opts)(agentOrchestrationSelected())
	if err != nil {
		t.Fatalf("сборка сервиса agent-orchestration: %v", err)
	}
	owner, ok := service.(*agentorchestration.Service)
	if !ok {
		t.Fatalf("ожидался *agentorchestration.Service, got %T", service)
	}
	// Первая версия опубликована разово (дубли тенантов схлопнуты): повторная
	// публикация той же версии отвергается доменом (неизменяемость).
	if _, err := owner.PublishPipeline(context.Background(), agentorchestration.PublishPipelineRequest{
		PipelineID: agentDefaultPipelineID,
		TenantID:   "tenant-1",
		Version:    1,
		Steps:      agentorchestration.DefaultPipelineSteps(),
	}); err == nil {
		t.Fatal("повторная публикация версии 1 должна быть отвергнута")
	}
}

// TestAssembleOperationsSourceNetwork проверяет выбор сетевого транспорта по
// заданному адресу владельца: лента и состояние запусков читаются клиентом
// контракта agent-run-state. Агрегаты остаются незаданными: адрес analytics не
// задан, владелец analytics не выбран.
func TestAssembleOperationsSourceNetwork(t *testing.T) {
	opts := buildOptions{agentOrchestrationURL: "http://agent-orchestration.example.invalid"}
	assembleOperationsSource(nil, &opts, func(selectedService) (composition.Service, error) {
		t.Fatal("владелец не должен собираться при сетевом транспорте")
		return nil, nil
	}, observ.NewLogger())

	if opts.operations.Feed == nil {
		t.Fatal("лента запусков не подключена")
	}
	if opts.operations.RunState == nil {
		t.Fatal("состояние запусков не подключено")
	}
	if opts.operations.Aggregates != nil {
		t.Fatal("агрегатор процесса не должен подключаться без адреса analytics и выбранного владельца")
	}
}

// TestAssembleOperationsSourceCoLocated проверяет in-proc связывание при
// co-location: co-located владелец разрешается лениво и отдаёт read-поверхность
// запусков (пустой список — корректный результат, а не ошибка).
func TestAssembleOperationsSourceCoLocated(t *testing.T) {
	opts := buildOptions{
		apiAddr:                ":0",
		agentWorkflowTaskQueue: agentorchestration.ServiceID,
		logger:                 observ.NewLogger(),
		metrics:                observ.NewMetrics(),
	}
	assembleOperationsSource([]selectedService{agentOrchestrationSelected()}, &opts, supportServiceBuildOnce(t, opts), observ.NewLogger())

	if opts.operations.Feed == nil || opts.operations.RunState == nil {
		t.Fatalf("поверхность запусков не связана: feed=%v runState=%v", opts.operations.Feed, opts.operations.RunState)
	}
	if opts.operations.Aggregates != nil {
		t.Fatal("владелец analytics не выбран — агрегатор процесса должен остаться незаданным")
	}
	rows, err := opts.operations.Feed.Runs(context.Background(), platformgovernance.AgentRunQuery{TenantID: "tenant-1", Limit: 10})
	if err != nil {
		t.Fatalf("ленивое разрешение ленты: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("лента пустого владельца = %+v", rows)
	}
	if _, err := opts.operations.RunState.RunState(context.Background(), agentrunstate.Request{RunID: "run-missing", TenantID: "tenant-1"}); !errors.Is(err, agentrunstate.ErrNotFound) {
		t.Fatalf("RunState отсутствующего запуска = %v, ожидалась ErrNotFound", err)
	}
}

// TestAssembleOperationsSourceUnavailable проверяет честную недоступность: без
// адресов и без выбранных владельцев все поверхности экрана остаются незаданными
// (экран покажет «недоступно», а не нули).
func TestAssembleOperationsSourceUnavailable(t *testing.T) {
	opts := buildOptions{}
	assembleOperationsSource(nil, &opts, func(selectedService) (composition.Service, error) {
		return nil, nil
	}, observ.NewLogger())

	if opts.operations.Feed != nil || opts.operations.RunState != nil || opts.operations.Aggregates != nil {
		t.Fatalf("поверхности операций должны остаться незаданными: %+v", opts.operations)
	}
}

// analyticsSelected — выбор сервиса analytics для тестов проводки агрегатов.
func analyticsSelected() selectedService {
	return selectedService{Process: "support", Mechanism: "in-process", ID: analytics.ServiceID}
}

// TestAssembleOperationsSourceAggregatesNetwork проверяет сетевой путь агрегатов:
// при заданном адресе analytics источник получает агрегатор поверх сетевого
// клиента контракта campaign-report и читает цифры процесса из формы wire.
func TestAssembleOperationsSourceAggregatesNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/operations/numbers" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"contract":"campaign-report","schema_version":"1.0.0",` +
			`"clients":[{"client_id":"tenant-1","found":9,"processed":5,"rejected":3,"in_progress":2,"result":1}],` +
			`"periods":[{"date":"2026-10-06","found":4,"processed":5,"rejected":3}],"count":1}`))
	}))
	t.Cleanup(server.Close)

	opts := buildOptions{analyticsURL: server.URL}
	assembleOperationsSource(nil, &opts, func(selectedService) (composition.Service, error) {
		t.Fatal("владелец не должен собираться при сетевом транспорте агрегатов")
		return nil, nil
	}, observ.NewLogger())

	if opts.operations.Aggregates == nil {
		t.Fatal("агрегатор процесса не подключён по сети")
	}
	numbers, err := opts.operations.Aggregates.Numbers(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Numbers по сети: %v", err)
	}
	if len(numbers.Clients) != 1 || numbers.Clients[0].Found != 9 || numbers.Clients[0].InProgress != 2 {
		t.Fatalf("цифры по сети = %+v", numbers.Clients)
	}
}

// TestAssembleOperationsSourceAggregatesCoLocated проверяет in-proc связывание
// агрегатов: co-located владелец analytics разрешается лениво и отдаёт
// опубликованный контракт campaign-report (пустая проекция — корректный
// результат, а не ошибка).
func TestAssembleOperationsSourceAggregatesCoLocated(t *testing.T) {
	opts := buildOptions{
		apiAddr: ":0",
		logger:  observ.NewLogger(),
		metrics: observ.NewMetrics(),
	}
	assembleOperationsSource([]selectedService{analyticsSelected()}, &opts, supportServiceBuildOnce(t, opts), observ.NewLogger())

	if opts.operations.Aggregates == nil {
		t.Fatal("агрегатор процесса не подключён при co-location")
	}
	numbers, err := opts.operations.Aggregates.Numbers(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Numbers co-located: %v", err)
	}
	if len(numbers.Clients) != 0 || len(numbers.Periods) != 0 {
		t.Fatalf("цифры пустой проекции = %+v", numbers)
	}
	clients, err := opts.operations.Aggregates.Clients(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Clients co-located: %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("агрегаты пустой проекции = %+v", clients)
	}
}

// TestAgentRunFeedMapsRunSummaries проверяет перевод сводки запуска владельца в
// строку ленты экрана: идентификаторы, код состояния и текущий шаг.
func TestAgentRunFeedMapsRunSummaries(t *testing.T) {
	lister := agentrunstate.InProc(agentrunstate.InProcHandler{
		ListFunc: func(_ context.Context, req agentrunstate.ListRequest) ([]agentrunstate.RunSummary, error) {
			if req.TenantID != "tenant-1" {
				t.Fatalf("tenant_id = %q", req.TenantID)
			}
			return []agentrunstate.RunSummary{{
				RunID:       "run-1",
				TenantID:    "tenant-1",
				CompanyID:   "company-1",
				Status:      "running",
				CurrentStep: "enrich",
			}}, nil
		},
	})
	rows, err := agentRunFeed{lister: lister}.Runs(context.Background(), platformgovernance.AgentRunQuery{TenantID: "tenant-1", Limit: 10})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("строк = %d, ожидалась 1", len(rows))
	}
	row := rows[0]
	if row.RunID != "run-1" || row.TenantID != "tenant-1" || row.CompanyID != "company-1" {
		t.Fatalf("идентификаторы строки = %+v", row)
	}
	if row.State != "running" || row.CurrentStep != "enrich" {
		t.Fatalf("состояние/шаг = %+v", row)
	}
}

// TestAgentRunFeedWithoutListerIsUnavailable проверяет честный отказ: без
// подключённого владельца лента недоступна, а не пустой успех.
func TestAgentRunFeedWithoutListerIsUnavailable(t *testing.T) {
	if _, err := (agentRunFeed{}).Runs(context.Background(), platformgovernance.AgentRunQuery{TenantID: "tenant-1"}); !errors.Is(err, platformgovernance.ErrOperationsUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrOperationsUnavailable", err)
	}
}
