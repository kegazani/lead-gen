package workflow

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.temporal.io/api/serviceerror"

	"lead-gen/backend/platform/observability/prommetrics"
)

// TestStartedRunRecognizesAlreadyStarted проверяет, что повторный старт
// трактуется идемпотентно: клиент возвращает ссылку на существующее исполнение с
// признаком «уже запущено», а не ошибку недоступности.
func TestStartedRunRecognizesAlreadyStarted(t *testing.T) {
	err := serviceerror.NewWorkflowExecutionAlreadyStarted("уже запущено", "start-req-1", "run-existing")
	run, ok := startedRun(err, "fulfillment-req-1")
	if !ok {
		t.Fatal("WorkflowExecutionAlreadyStarted не распознан как повторный старт")
	}
	if !run.AlreadyStarted || run.WorkflowID != "fulfillment-req-1" || run.RunID != "run-existing" {
		t.Fatalf("ссылка на исполнение = %+v", run)
	}
}

// TestStartedRunIgnoresOtherErrors проверяет, что прочие ошибки не выдаются за
// повторный старт: настоящая недоступность рантайма остаётся ошибкой.
func TestStartedRunIgnoresOtherErrors(t *testing.T) {
	var sentinel = errors.New("рантайм недоступен")
	if _, ok := startedRun(sentinel, "fulfillment-req-1"); ok {
		t.Fatal("обычная ошибка не должна трактоваться как повторный старт")
	}
}

// TestWorkflowMetricNamesRegistered — инвариант: каждое имя метрики, эмитируемое
// клиентом через порт observ.Metrics, объявлено в каталоге prommetrics.specs.
// Имя вне каталога молча отбрасывается адаптером (белый список имён), поэтому
// рассинхронизация имён означала бы тихую потерю счётчиков запуска workflow.
func TestWorkflowMetricNamesRegistered(t *testing.T) {
	emitted := []string{
		metricWorkflowStarted,
		metricWorkflowStartError,
		metricWorkflowStartAlreadyStarted,
	}

	registered := make(map[string]bool, len(prommetrics.MetricNames()))
	for _, name := range prommetrics.MetricNames() {
		registered[name] = true
	}

	for _, name := range emitted {
		if !registered[name] {
			t.Errorf("метрика %q эмитится клиентом, но отсутствует в каталоге prommetrics.specs и будет молча отброшена", name)
		}
	}
}

// TestWorkflowCountersVisibleInMetrics проверяет сквозной путь: счётчики запуска
// workflow, учтённые через адаптер, видны в /metrics без высококардинальной
// метки workflow_id.
func TestWorkflowCountersVisibleInMetrics(t *testing.T) {
	adapter := prommetrics.New()
	c := &Client{metrics: adapter}

	c.count(metricWorkflowStarted, nil)
	c.count(metricWorkflowStartError, nil)
	c.count(metricWorkflowStartAlreadyStarted, nil)

	rec := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()

	for _, name := range []string{
		metricWorkflowStarted,
		metricWorkflowStartError,
		metricWorkflowStartAlreadyStarted,
	} {
		if !strings.Contains(body, name) {
			t.Errorf("тело /metrics не содержит %q:\n%s", name, body)
		}
	}
	// Идентификатор исполнения не должен попадать в экспозицию метрик.
	if strings.Contains(body, "workflow_id") {
		t.Errorf("высококардинальная метка workflow_id попала в /metrics:\n%s", body)
	}
}
