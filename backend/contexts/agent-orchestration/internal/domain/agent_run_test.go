package domain

import (
	"errors"
	"testing"
)

func TestNewRunBindsPipelineVersion(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"), spec("s2", "classify"))
	run := testRun(t, pipeline)

	if run.Status() != StatusQueued {
		t.Fatalf("начальный статус = %q, ожидался %q", run.Status(), StatusQueued)
	}
	if run.PipelineVersion() != pipeline.Version() || run.PipelineID() != pipeline.ID() {
		t.Fatalf("привязка версии: version=%d id=%q", run.PipelineVersion(), run.PipelineID())
	}
	if len(run.Steps()) != 2 {
		t.Fatalf("число шагов = %d, ожидалось 2", len(run.Steps()))
	}
	if run.TenantID() != "tenant-1" {
		t.Fatalf("тенант запуска = %q", run.TenantID())
	}
}

func TestNewRunRejectsForeignPipelineTenant(t *testing.T) {
	registry := testRegistry(t)
	pipeline, err := NewPipeline("pipe-1", "tenant-other", 1, []StepSpec{spec("s1", "search")}, registry)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := pipeline.Publish(baseTime, header("evt-publish")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	mission, _ := NewAgentMission("mission-1", "company-1", "tenant-1", "")
	if _, err := NewRun(NewRunParams{ID: "run-1", Mission: mission, Pipeline: pipeline, At: baseTime, Header: header("evt-run")}); err == nil {
		t.Fatal("запуск по пайплайну чужого тенанта должен быть отвергнут")
	}
}

func TestNewRunRequiresPublishedPipeline(t *testing.T) {
	registry := testRegistry(t)
	draft, err := NewPipeline("pipe-1", "tenant-1", 1, []StepSpec{spec("s1", "search")}, registry)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	mission, _ := NewAgentMission("mission-1", "company-1", "tenant-1", "")
	if _, err := NewRun(NewRunParams{ID: "run-1", Mission: mission, Pipeline: draft, At: baseTime, Header: header("evt-run")}); !errors.Is(err, ErrPipelineNotPublished) {
		t.Fatalf("ошибка = %v, ожидалась ErrPipelineNotPublished", err)
	}
}

func TestRunStartAndTerminality(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"))
	run := testRun(t, pipeline)

	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.Status() != StatusRunning {
		t.Fatalf("статус после старта = %q", run.Status())
	}

	if _, _, err := run.BeginStep("s1", at(1)); err != nil {
		t.Fatalf("BeginStep: %v", err)
	}
	if _, err := run.CompleteStep("s1", Succeeded(), "", "", 0, at(2), header("evt-step")); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}
	if run.Status() != StatusCompleted {
		t.Fatalf("статус после последнего шага = %q, ожидался %q", run.Status(), StatusCompleted)
	}
	if !run.Status().IsTerminal() {
		t.Fatal("completed должен быть терминальным")
	}

	// Терминальное состояние не возобновляется.
	if err := run.Start(at(3), header("evt-start-2")); !errors.Is(err, ErrTerminalRun) {
		t.Fatalf("повторный старт терминального запуска: ошибка = %v", err)
	}
	if err := run.Stop("actor-1", "stop", at(3), header("evt-stop")); !errors.Is(err, ErrTerminalRun) {
		t.Fatalf("остановка терминального запуска: ошибка = %v", err)
	}
}

func TestRunInvalidTransitionRejected(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"))
	run := testRun(t, pipeline)

	// Шаг нельзя начать до старта запуска: queued → running обязателен.
	if _, _, err := run.BeginStep("s1", at(1)); !errors.Is(err, ErrInvalidRunTransition) {
		t.Fatalf("шаг до старта: ошибка = %v, ожидалась ErrInvalidRunTransition", err)
	}
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Исход нельзя зафиксировать на незапущенном шаге.
	if _, err := run.CompleteStep("s1", Succeeded(), "", "", 0, at(2), header("evt-step")); !errors.Is(err, ErrStepNotStarted) {
		t.Fatalf("закрытие незапущенного шага: ошибка = %v, ожидалась ErrStepNotStarted", err)
	}
}

func TestStopRunReachesTerminalStopped(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"), spec("s2", "classify"))
	run := testRun(t, pipeline)
	if err := run.Start(baseTime, header("evt-start")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := run.Stop("actor-1", "оператор остановил", at(1), header("evt-stop")); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if run.Status() != StatusStopped || !run.Status().IsTerminal() {
		t.Fatalf("статус = %q, ожидался терминальный %q", run.Status(), StatusStopped)
	}
	if _, ok := run.CurrentStep(); ok {
		t.Fatal("остановленный запуск не должен иметь текущего шага")
	}
}

func TestRunIsolatedByTenantOnRehydrate(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"))
	run := testRun(t, pipeline)

	snapshot := run.Snapshot()
	restored, err := RehydrateRun(snapshot)
	if err != nil {
		t.Fatalf("RehydrateRun: %v", err)
	}
	if restored.TenantID() != run.TenantID() {
		t.Fatalf("тенант восстановленного запуска = %q", restored.TenantID())
	}
}
