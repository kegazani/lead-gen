package workflow

import (
	"errors"
	"testing"

	"go.temporal.io/api/serviceerror"
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
