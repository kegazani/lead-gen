package domain

import (
	"errors"
	"testing"
)

// stepConfigWithAttempts собирает конфигурацию шага с заданным пределом попыток.
func stepConfigWithAttempts(t *testing.T, attempts int) StepConfig {
	t.Helper()
	config, err := NewStepConfig(nil, attempts)
	if err != nil {
		t.Fatalf("NewStepConfig: %v", err)
	}
	return config
}

func TestStepIdempotencyNoDoubleEffect(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"), spec("s2", "classify"))
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	run.PullDomainEvents()

	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep: %v", err)
	}
	if _, err := run.CompleteStep("s1", Succeeded(), "lead-1", "", 0, at(2), header("evt-step-1")); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}
	cursorAfterFirst := run.Cursor()

	// Повторный прогон того же шага не даёт второго эффекта.
	_, alreadyDone, err := run.BeginStep("s1", at(3))
	if err != nil {
		t.Fatalf("BeginStep повтор: %v", err)
	}
	if !alreadyDone {
		t.Fatal("повтор шага после успеха должен сообщать alreadyDone")
	}
	repeat, err := run.CompleteStep("s1", Succeeded(), "lead-1", "", 0, at(4), header("evt-step-2"))
	if err != nil {
		t.Fatalf("CompleteStep повтор: %v", err)
	}
	if !repeat.Repeated {
		t.Fatal("повторный CompleteStep должен сообщать Repeated")
	}
	if run.Cursor() != cursorAfterFirst {
		t.Fatalf("повтор сдвинул курсор: %d → %d", cursorAfterFirst, run.Cursor())
	}

	completed := 0
	for _, event := range run.DomainEvents() {
		if event.Name() == EventStepCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("событий agent-step-completed = %d, ожидалось 1 (эффект один раз)", completed)
	}
}

func TestStepEventCarriesRunBlockTimeOutcome(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"))
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	run.PullDomainEvents()

	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep: %v", err)
	}
	if _, err := run.CompleteStep("s1", Succeeded(), "lead-1", "", 0, at(2), header("evt-step")); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}

	var stepEvent StepCompleted
	found := false
	for _, event := range run.DomainEvents() {
		if completed, ok := event.(StepCompleted); ok {
			stepEvent, found = completed, true
		}
	}
	if !found {
		t.Fatal("событие agent-step-completed не зафиксировано")
	}
	if stepEvent.RunID != run.ID() {
		t.Fatalf("событие без agent_run_id: %q", stepEvent.RunID)
	}
	if stepEvent.BlockRef != "search" {
		t.Fatalf("событие без блока: %q", stepEvent.BlockRef)
	}
	if !stepEvent.Header().OccurredAt.Equal(at(2)) {
		t.Fatalf("событие без времени: %s", stepEvent.Header().OccurredAt)
	}
	if !stepEvent.Outcome.IsSuccess() {
		t.Fatalf("исход события = %q", stepEvent.Outcome.Kind())
	}
}

func TestFailStepBoundedByAttempts(t *testing.T) {
	registry := testRegistry(t)
	config := stepConfigWithAttempts(t, 2)
	pipeline := publishedPipeline(t, registry, StepSpec{ID: "s1", Ref: "search", Config: config})
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep: %v", err)
	}
	retryable, err := run.FailStep("s1", "недоступность источника", at(2), header("evt-fail-1"))
	if err != nil {
		t.Fatalf("FailStep: %v", err)
	}
	if !retryable {
		t.Fatal("первый отказ должен быть повторяемым")
	}
	if run.Status() != StatusRunning {
		t.Fatalf("статус после повторяемого отказа = %q", run.Status())
	}

	if _, _, err := run.BeginStep("s1", at(3)); err != nil {
		t.Fatalf("BeginStep повтор: %v", err)
	}
	retryable, err = run.FailStep("s1", "недоступность источника", at(4), header("evt-fail-2"))
	if err != nil {
		t.Fatalf("FailStep повтор: %v", err)
	}
	if retryable {
		t.Fatal("исчерпание попыток должно остановить повтор")
	}
	if run.Status() != StatusNeedsAttention || !run.Status().IsTerminal() {
		t.Fatalf("статус после исчерпания попыток = %q, ожидался терминальный %q", run.Status(), StatusNeedsAttention)
	}
}

func TestIrreversibleStepRequiresApproval(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "await-approval"), spec("s2", "send"))
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Проходим шаг подготовки черновика.
	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep prepare: %v", err)
	}
	if _, err := run.CompleteStep("s1", Succeeded(), "", "draft-1", 1, at(2), header("evt-prepare")); err != nil {
		t.Fatalf("CompleteStep prepare: %v", err)
	}

	// Отправка без решения оператора невозможна.
	if _, _, err := run.BeginStep("s2", at(3)); err != nil {
		t.Fatalf("BeginStep send: %v", err)
	}
	if _, err := run.CompleteStep("s2", Succeeded(), "", "draft-1", 1, at(4), header("evt-send")); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("отправка без решения: ошибка = %v, ожидалась ErrApprovalRequired", err)
	}
}

func TestApprovalBindsExactDraftVersion(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "await-approval"), spec("s2", "send"))
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep prepare: %v", err)
	}
	if _, err := run.CompleteStep("s1", Succeeded(), "", "draft-1", 1, at(2), header("evt-prepare")); err != nil {
		t.Fatalf("CompleteStep prepare: %v", err)
	}
	if err := run.RequestApproval("draft-1", 1, "actor-1", at(3), header("evt-req")); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if run.Status() != StatusAwaitingApproval {
		t.Fatalf("статус = %q, ожидался %q", run.Status(), StatusAwaitingApproval)
	}

	// Изменённый черновик требует нового решения.
	if err := run.GrantApproval("draft-2", 1, "actor-1", at(4), header("evt-grant")); !errors.Is(err, ErrDraftChanged) {
		t.Fatalf("решение по другому черновику: ошибка = %v, ожидалась ErrDraftChanged", err)
	}
	if err := run.GrantApproval("draft-1", 1, "actor-1", at(5), header("evt-grant")); err != nil {
		t.Fatalf("GrantApproval: %v", err)
	}
	if run.Status() != StatusRunning {
		t.Fatalf("статус после решения = %q, ожидался %q", run.Status(), StatusRunning)
	}

	// Отправка идёт по утверждённой версии и завершает запуск.
	if _, _, err := run.BeginStep("s2", at(6)); err != nil {
		t.Fatalf("BeginStep send: %v", err)
	}
	if _, err := run.CompleteStep("s2", Succeeded(), "", "draft-1", 1, at(7), header("evt-send")); err != nil {
		t.Fatalf("CompleteStep send: %v", err)
	}
	if run.Status() != StatusCompleted {
		t.Fatalf("статус после отправки = %q, ожидался %q", run.Status(), StatusCompleted)
	}
}

func TestApprovalRejectionHoldsRunWithoutSending(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "await-approval"), spec("s2", "send"))
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep prepare: %v", err)
	}
	if _, err := run.CompleteStep("s1", Succeeded(), "", "draft-1", 1, at(2), header("evt-prepare")); err != nil {
		t.Fatalf("CompleteStep prepare: %v", err)
	}
	if err := run.RequestApproval("draft-1", 1, "actor-1", at(3), header("evt-req")); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if err := run.RejectApproval("actor-1", "содержание не подходит", at(4), header("evt-reject")); err != nil {
		t.Fatalf("RejectApproval: %v", err)
	}
	if run.Status() != StatusNeedsAttention || !run.Status().IsTerminal() {
		t.Fatalf("статус после отказа = %q, ожидался терминальный %q", run.Status(), StatusNeedsAttention)
	}
	// Письмо не отправлено: шаг отправки не исполнялся.
	if step, ok := run.CurrentStep(); ok && step.Status() == StepRunSucceeded {
		t.Fatal("отправка не должна исполняться после отказа оператора")
	}
}

func TestUnavailableMandatoryStepHoldsRun(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"), spec("s2", "classify"))
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	run.PullDomainEvents()

	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep: %v", err)
	}
	blocked, err := run.MarkStepUnavailable("s1", "провайдер поиска не выбран", at(2), header("evt-unavailable"))
	if err != nil {
		t.Fatalf("MarkStepUnavailable: %v", err)
	}
	if !blocked {
		t.Fatal("недоступность обязательного шага должна держать запуск незавершённым")
	}
	if run.Status() != StatusBlocked || run.Status().IsTerminal() {
		t.Fatalf("статус = %q, ожидался незавершённый %q", run.Status(), StatusBlocked)
	}

	// Недоступность не подменяется успехом и не закрывает процесс успехом.
	var sawUnavailable bool
	for _, event := range run.DomainEvents() {
		if failed, ok := event.(StepFailed); ok && failed.Outcome.IsUnavailable() {
			sawUnavailable = true
		}
		if event.Name() == EventRunCompleted {
			t.Fatal("недоступный обязательный шаг не должен завершать процесс успехом")
		}
	}
	if !sawUnavailable {
		t.Fatal("недоступность не зафиксирована явным исходом unavailable")
	}
}
