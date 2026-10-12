package platformgovernance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

// fakeFeed — тестовый двойник read-поверхности ленты агентов (владелец
// agent-orchestration). В поставке при отсутствии владельца поле остаётся nil.
type fakeFeed struct {
	runs  []platformgovernance.AgentRunSummary
	query platformgovernance.AgentRunQuery
	err   error
}

func (f *fakeFeed) Runs(_ context.Context, query platformgovernance.AgentRunQuery) ([]platformgovernance.AgentRunSummary, error) {
	f.query = query
	return f.runs, f.err
}

// fakeAggregates — тестовый двойник агрегатов процесса (владелец analytics).
type fakeAggregates struct {
	numbers httpapi.ClientNumbersResult
	clients []httpapi.ClientAggregate
	err     error
}

func (f fakeAggregates) Numbers(context.Context, time.Duration) (httpapi.ClientNumbersResult, error) {
	return f.numbers, f.err
}

func (f fakeAggregates) Clients(context.Context, time.Duration) ([]httpapi.ClientAggregate, error) {
	return f.clients, f.err
}

// TestOperationsSourceAgentDetail проверяет перевод опубликованного контракта
// agent-run-state в подробности экрана: события только по завершённым шагам, исход
// владельца переводится, отказ и недоступность не маскируются под успех, причины
// агрегируются и упорядочены.
func TestOperationsSourceAgentDetail(t *testing.T) {
	reader := agentrunstate.InProcHandler{RunStateFunc: func(_ context.Context, request agentrunstate.Request) (agentrunstate.RunState, error) {
		if request.RunID != "run-1" || request.TenantID != "tenant-1" {
			t.Fatalf("запрос контракта = %+v", request)
		}
		return agentrunstate.RunState{
			RunID:     "run-1",
			TenantID:  "tenant-1",
			CompanyID: "company-1",
			Status:    "needs-attention",
			Steps: []agentrunstate.StepState{
				{StepID: "search", BlockRef: "search", Outcome: "succeeded"},
				{StepID: "classify", BlockRef: "classify", Outcome: "failed", Reason: "extraction_error"},
				{StepID: "enrich", BlockRef: "enrich", Outcome: "failed", Reason: "extraction_error"},
				{StepID: "verify-email", BlockRef: "verify-email", Outcome: "unavailable", Reason: "provider_unavailable"},
				{StepID: "send", BlockRef: "send"}, // шаг не завершён: события нет
			},
		}, nil
	}}
	source := platformgovernance.NewOperationsSource(platformgovernance.OperationsInput{RunState: reader})

	detail, err := source.AgentDetail(context.Background(), "tenant-1", "run-1")
	if err != nil {
		t.Fatalf("AgentDetail: %v", err)
	}
	if detail.RunID != "run-1" || detail.ClientID != "tenant-1" || detail.Company != "company-1" || detail.State != "needs-attention" {
		t.Fatalf("поля подробностей = %+v", detail)
	}
	if len(detail.Events) != 4 {
		t.Fatalf("событий = %d, ожидалось 4 (незавершённый шаг без исхода пропущен)", len(detail.Events))
	}
	wantOutcomes := []string{"completed", "failed", "failed", "unavailable"}
	for i, event := range detail.Events {
		if event.Outcome != wantOutcomes[i] {
			t.Errorf("событие %d: исход = %q, ожидался %q", i, event.Outcome, wantOutcomes[i])
		}
		if event.Kind != "activity" {
			t.Errorf("событие %d: вид = %q, ожидался activity", i, event.Kind)
		}
	}
	// Отказ шага показан отказом, недоступность — отдельным честным исходом.
	if len(detail.Reasons) != 2 {
		t.Fatalf("причин = %d, ожидалось 2", len(detail.Reasons))
	}
	if detail.Reasons[0].Code != "extraction_error" || detail.Reasons[0].Count != 2 {
		t.Errorf("первая причина = %+v, ожидалось extraction_error×2", detail.Reasons[0])
	}
	if detail.Reasons[1].Code != "provider_unavailable" || detail.Reasons[1].Count != 1 {
		t.Errorf("вторая причина = %+v, ожидалось provider_unavailable×1", detail.Reasons[1])
	}
}

// TestOperationsSourceAgents проверяет перевод ленты: клиент берётся из клиента или
// тенанта, компания — из названия или идентификатора, фильтр передаётся владельцу.
func TestOperationsSourceAgents(t *testing.T) {
	feed := &fakeFeed{runs: []platformgovernance.AgentRunSummary{
		{RunID: "run-1", TenantID: "tenant-1", Company: "ООО Ромашка", State: "running", CurrentStep: "enrich", UpdatedAt: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)},
		{RunID: "run-2", TenantID: "tenant-1", ClientID: "client-2", CompanyID: "company-2", State: "queued"},
	}}
	source := platformgovernance.NewOperationsSource(platformgovernance.OperationsInput{Feed: feed})

	rows, err := source.Agents(context.Background(), httpapi.AgentFilter{TenantID: "tenant-1", Category: "in_progress", Limit: 50})
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if feed.query.TenantID != "tenant-1" || feed.query.Category != "in_progress" || feed.query.Limit != 50 {
		t.Fatalf("фильтр владельцу = %+v", feed.query)
	}
	if len(rows) != 2 {
		t.Fatalf("строк = %d, ожидалось 2", len(rows))
	}
	if rows[0].ClientID != "tenant-1" || rows[0].Company != "ООО Ромашка" {
		t.Errorf("строка 0 = %+v", rows[0])
	}
	if rows[1].ClientID != "client-2" || rows[1].Company != "company-2" {
		t.Errorf("строка 1 = %+v", rows[1])
	}
}

// TestOperationsSourceAggregates проверяет цифры процесса и агрегаты дашборда от
// владельца аналитики.
func TestOperationsSourceAggregates(t *testing.T) {
	aggregates := fakeAggregates{
		numbers: httpapi.ClientNumbersResult{
			Clients: []httpapi.ClientNumbers{{ClientID: "tenant-1", Found: 3, Rejected: 1}},
			Periods: []httpapi.NumberPeriod{{Date: "2026-10-06", Found: 3, Rejected: 1}},
		},
		clients: []httpapi.ClientAggregate{{ClientID: "tenant-1", Found: 3, Anomalies: []string{"stalled"}}},
	}
	source := platformgovernance.NewOperationsSource(platformgovernance.OperationsInput{Aggregates: aggregates})

	numbers, err := source.Numbers(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if len(numbers.Clients) != 1 || numbers.Clients[0].Found != 3 {
		t.Fatalf("цифры = %+v", numbers)
	}
	if len(numbers.Periods) != 1 || numbers.Periods[0].Date != "2026-10-06" {
		t.Fatalf("разбивка = %+v", numbers.Periods)
	}
	clients, err := source.Clients(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Clients: %v", err)
	}
	if len(clients) != 1 || clients[0].ClientID != "tenant-1" {
		t.Fatalf("агрегаты = %+v", clients)
	}
}

// TestOperationsSourceUnavailable проверяет честную недоступность: незаданная
// поверхность владельца даёт ошибку, а не нули и не пустой успех.
func TestOperationsSourceUnavailable(t *testing.T) {
	source := platformgovernance.NewOperationsSource(platformgovernance.OperationsInput{})
	if _, err := source.Numbers(context.Background(), time.Hour); !errors.Is(err, platformgovernance.ErrOperationsUnavailable) {
		t.Errorf("Numbers: err = %v, ожидалась недоступность", err)
	}
	if _, err := source.Clients(context.Background(), time.Hour); !errors.Is(err, platformgovernance.ErrOperationsUnavailable) {
		t.Errorf("Clients: err = %v, ожидалась недоступность", err)
	}
	if _, err := source.Agents(context.Background(), httpapi.AgentFilter{TenantID: "tenant-1"}); !errors.Is(err, platformgovernance.ErrOperationsUnavailable) {
		t.Errorf("Agents: err = %v, ожидалась недоступность", err)
	}
	if _, err := source.AgentDetail(context.Background(), "tenant-1", "run-1"); !errors.Is(err, platformgovernance.ErrOperationsUnavailable) {
		t.Errorf("AgentDetail: err = %v, ожидалась недоступность", err)
	}
}

// TestOperationsSourceAgentDetailValidatesTenant проверяет, что чтение запуска
// скоупится тенантом: без тенанта запрос отвергается.
func TestOperationsSourceAgentDetailValidatesTenant(t *testing.T) {
	reader := agentrunstate.InProcHandler{RunStateFunc: func(context.Context, agentrunstate.Request) (agentrunstate.RunState, error) {
		t.Fatal("владелец не должен вызываться без тенанта")
		return agentrunstate.RunState{}, nil
	}}
	source := platformgovernance.NewOperationsSource(platformgovernance.OperationsInput{RunState: reader})
	if _, err := source.AgentDetail(context.Background(), "", "run-1"); err == nil {
		t.Fatal("пустой тенант не отвергнут")
	}
}
