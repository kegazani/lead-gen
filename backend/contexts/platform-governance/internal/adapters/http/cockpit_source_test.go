package httpapi_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/backend/platform/workflow"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

// Тесты экрана операций с реальным источником (platformgovernance.NewOperationsSource):
// проверяют сквозную сборку данных источника, честную недоступность и разметку
// авто-обновляемых фрагментов. Тестовые двойники стоят на границе владельцев
// (agent-orchestration/analytics), а не на пути продуктовой функции.

// fixedFeed — двойник ленты агентов владельца agent-orchestration.
type fixedFeed struct {
	runs []platformgovernance.AgentRunSummary
}

func (f fixedFeed) Runs(context.Context, platformgovernance.AgentRunQuery) ([]platformgovernance.AgentRunSummary, error) {
	return f.runs, nil
}

// fixedAggregates — двойник агрегатов процесса владельца analytics.
type fixedAggregates struct {
	numbers httpapi.ClientNumbersResult
	clients []httpapi.ClientAggregate
}

func (f fixedAggregates) Numbers(context.Context, time.Duration) (httpapi.ClientNumbersResult, error) {
	return f.numbers, nil
}

func (f fixedAggregates) Clients(context.Context, time.Duration) ([]httpapi.ClientAggregate, error) {
	return f.clients, nil
}

// feedMoment — фиксированное время строки ленты для детерминированного golden.
var feedMoment = time.Date(2026, 10, 6, 12, 30, 0, 0, time.UTC)

// runStateReader — двойник опубликованного контракта agent-run-state (in-proc).
func runStateReader() agentrunstate.InProcHandler {
	return agentrunstate.InProcHandler{RunStateFunc: func(_ context.Context, request agentrunstate.Request) (agentrunstate.RunState, error) {
		return agentrunstate.RunState{
			RunID:     request.RunID,
			TenantID:  request.TenantID,
			CompanyID: "company-1",
			Status:    "needs-attention",
			Steps: []agentrunstate.StepState{
				{StepID: "search", BlockRef: "search", Outcome: "succeeded"},
				{StepID: "classify", BlockRef: "classify", Outcome: "failed", Reason: "extraction_error"},
				{StepID: "verify-email", BlockRef: "verify-email", Outcome: "unavailable", Reason: "provider_unavailable"},
			},
		}, nil
	}}
}

// realOperationsSource собирает реальный источник поверх тестовых владельцев.
func realOperationsSource() httpapi.OperationsData {
	return platformgovernance.NewOperationsSource(platformgovernance.OperationsInput{
		Feed: fixedFeed{runs: []platformgovernance.AgentRunSummary{
			{RunID: "run-1", TenantID: "tenant-1", Company: "ООО Ромашка", State: "running", CurrentStep: "enrich", UpdatedAt: feedMoment},
		}},
		RunState: runStateReader(),
		Aggregates: fixedAggregates{
			numbers: httpapi.ClientNumbersResult{
				Clients: []httpapi.ClientNumbers{{ClientID: "tenant-1", Found: 7, Processed: 4, Rejected: 2, InProgress: 1}},
				Periods: []httpapi.NumberPeriod{{Date: "2026-10-06", Found: 7, Processed: 4, Rejected: 2}},
			},
			clients: []httpapi.ClientAggregate{{ClientID: "tenant-1", Found: 7, Processed: 4, Anomalies: []string{"stalled"}}},
		},
	})
}

// TestCockpitOperationsRealSource проверяет сквозную сборку данных реальным
// источником: цифры, лента, подробности из контракта и дашборд.
func TestCockpitOperationsRealSource(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: realOperationsSource(), CockpitMinGroup: 1})

	numbers := body(t, get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{"tenant-1", "7</a>", "2</a>"} {
		if !strings.Contains(numbers, want) {
			t.Fatalf("экран цифр не содержит %q:\n%s", want, numbers)
		}
	}

	agents := body(t, get(t, server, "/platform/operations/agents?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{"agent-row-run-1", "ООО Ромашка", "выполняется", "enrich"} {
		if !strings.Contains(agents, want) {
			t.Fatalf("лента не содержит %q:\n%s", want, agents)
		}
	}

	detail := body(t, get(t, server, "/platform/operations/agents/run-1?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{
		"company-1",
		"ошибка извлечения",
		"провайдер недоступен",
		"недоступно", // исход unavailable не маскируется под успех
		"требует вмешательства",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("подробности не содержат %q:\n%s", want, detail)
		}
	}

	dashboard := body(t, get(t, server, "/platform/operations/dashboard?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{"tenant-1", "процесс замер"} {
		if !strings.Contains(dashboard, want) {
			t.Fatalf("дашборд не содержит %q:\n%s", want, dashboard)
		}
	}
}

// TestCockpitOperationsSourceHonestUnavailable проверяет, что неполный источник
// (владелец не подключён) даёт честное «недоступно» в своём разделе, а не нули и не
// пустой успех.
func TestCockpitOperationsSourceHonestUnavailable(t *testing.T) {
	empty := platformgovernance.NewOperationsSource(platformgovernance.OperationsInput{})
	server := newTopologyServer(t, platformgovernance.Config{Operations: empty})

	cases := []struct {
		path string
		want string
	}{
		{"/platform/operations?tenant_id=tenant-1&actor=user-1", "Данные процесса недоступны"},
		{"/platform/operations/agents?tenant_id=tenant-1&actor=user-1", "Лента агентов недоступна"},
		{"/platform/operations/agents/run-1?tenant_id=tenant-1&actor=user-1", "Подробности недоступны"},
		{"/platform/operations/dashboard?tenant_id=tenant-1&actor=user-1", "Агрегаты недоступны"},
	}
	for _, tc := range cases {
		page := body(t, get(t, server, tc.path))
		if !strings.Contains(page, tc.want) {
			t.Errorf("%s: нет честного «недоступно» (%q):\n%s", tc.path, tc.want, page)
		}
	}
}

// TestCockpitLiveA11yMarkup проверяет разметку авто-обновляемых фрагментов: лента и
// граф несут указание вернуть фокус и живое объявление (console-a11y.js).
func TestCockpitLiveA11yMarkup(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: realOperationsSource()})
	agents := body(t, get(t, server, "/platform/operations/agents?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{`data-focus-after-swap="#agents-feed"`, `data-live-announce="Лента агентов обновлена"`} {
		if !strings.Contains(agents, want) {
			t.Errorf("лента не содержит %q:\n%s", want, agents)
		}
	}

	processServer := newTopologyServer(t, platformgovernance.Config{WorkflowReader: fakeWorkflow{executions: []workflow.ProcessExecution{{WorkflowID: "wf-1", RunID: "run-1", Type: "AgentRun", Status: workflow.ProcessRunning}}}})
	process := body(t, get(t, processServer, "/platform/operations/process?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{`data-focus-after-swap="#process-graph"`, `data-live-announce="Граф процессов обновлён"`} {
		if !strings.Contains(process, want) {
			t.Errorf("граф не содержит %q:\n%s", want, process)
		}
	}
}

// csrfPlaceholder подменяет случайный токен CSRF в отрисовке: в золотом файле
// экрана операций он не должен быть недетерминированным.
const csrfPlaceholder = `name="csrf" value="csrf-token"`

// normalizeCSRF заменяет значение токена CSRF на фиксированное.
func normalizeCSRF(page string) string {
	return csrfFieldRe.ReplaceAllString(page, csrfPlaceholder)
}

// TestCockpitNumbersGolden сравнивает отрисовку экрана цифр процесса с золотым
// файлом: реальный источник (тестовый владелец analytics) наполняет цифры и
// разбивку по времени.
func TestCockpitNumbersGolden(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: realOperationsSource(), CockpitMinGroup: 1})
	page := body(t, get(t, server, "/platform/operations?tenant_id=tenant-1&actor=user-1"))
	checkGolden(t, "cockpit-numbers.golden", []byte(normalizeCSRF(page)))
}

// TestCockpitDashboardGolden сравнивает отрисовку полноэкранного дашборда с
// золотым файлом: агрегаты по клиентам и коды аномалий.
func TestCockpitDashboardGolden(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: realOperationsSource(), CockpitMinGroup: 1})
	page := body(t, get(t, server, "/platform/operations/dashboard?tenant_id=tenant-1&actor=user-1"))
	checkGolden(t, "cockpit-dashboard.golden", []byte(page))
}

// TestCockpitAgentsGolden сравнивает отрисовку живой ленты с золотым файлом.
func TestCockpitAgentsGolden(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: realOperationsSource()})
	page := body(t, get(t, server, "/platform/operations/agents?tenant_id=tenant-1&actor=user-1"))
	checkGolden(t, "cockpit-agents.golden", []byte(page))
}

// TestCockpitAgentDetailGolden сравнивает отрисовку подробностей агента с золотым
// файлом.
func TestCockpitAgentDetailGolden(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Operations: realOperationsSource()})
	page := body(t, get(t, server, "/platform/operations/agents/run-1?tenant_id=tenant-1&actor=user-1"))
	checkGolden(t, "cockpit-agent.golden", []byte(page))
}

// checkGolden сравнивает отрисовку с золотым файлом; с -update перезаписывает его.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("создание testdata: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("запись золотого файла: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("чтение золотого файла %s: %v; запустите тест с -update", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("рендер экрана не совпадает с %s\n--- получено ---\n%s\n--- ожидалось ---\n%s", golden, got, want)
	}
}
