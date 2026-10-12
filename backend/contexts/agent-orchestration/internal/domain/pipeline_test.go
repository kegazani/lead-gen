package domain

import (
	"errors"
	"testing"
	"time"
)

// baseTime — детерминированная точка отсчёта тестов домена.
var baseTime = time.Date(2026, time.February, 3, 10, 0, 0, 0, time.UTC)

// at возвращает момент времени, сдвинутый на minutes минут от baseTime.
func at(minutes int) time.Time { return baseTime.Add(time.Duration(minutes) * time.Minute) }

// testRegistry собирает реестр блоков, покрывающий пайплайн из спеки.
func testRegistry(t *testing.T) *BlockRegistry {
	t.Helper()
	specs := []struct {
		ref          BlockRef
		contract     string
		irreversible bool
		mandatory    bool
	}{
		{"search", "lead-search", false, true},
		{"classify", "lead-relevance", false, true},
		{"generate-email", "email-generation", false, false},
		{"await-approval", "campaign-command", false, false},
		{"send", "delivery-command", true, true},
		{"observe", "engagement-event", false, false},
	}
	blocks := make([]Block, 0, len(specs))
	for _, spec := range specs {
		block, err := NewBlock(spec.ref, string(spec.ref), spec.contract, spec.irreversible, spec.mandatory)
		if err != nil {
			t.Fatalf("сборка блока %s: %v", spec.ref, err)
		}
		blocks = append(blocks, block)
	}
	registry, err := NewBlockRegistry(blocks...)
	if err != nil {
		t.Fatalf("сборка реестра: %v", err)
	}
	return registry
}

// spec собирает описание шага с пустой конфигурацией.
func spec(id string, ref BlockRef) StepSpec {
	config, _ := NewStepConfig(nil, 0)
	return StepSpec{ID: PipelineStepID(id), Ref: ref, Config: config}
}

// header собирает конверт события с заданным event_id.
func header(eventID string) EventHeader {
	return EventHeader{EventID: EventID(eventID), CorrelationID: "corr-1"}
}

// publishedPipeline собирает опубликованную версию пайплайна.
func publishedPipeline(t *testing.T, registry *BlockRegistry, specs ...StepSpec) *AgentPipeline {
	t.Helper()
	pipeline, err := NewPipeline("pipe-1", "tenant-1", 1, specs, registry)
	if err != nil {
		t.Fatalf("сборка пайплайна: %v", err)
	}
	if err := pipeline.Publish(baseTime, header("evt-publish")); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}
	return pipeline
}

// testRun собирает запуск по опубликованному пайплайну.
func testRun(t *testing.T, pipeline *AgentPipeline) *AgentRun {
	t.Helper()
	mission, err := NewAgentMission("mission-1", "company-1", "tenant-1", "ищем IT-компании")
	if err != nil {
		t.Fatalf("сборка миссии: %v", err)
	}
	run, err := NewRun(NewRunParams{
		ID:       "run-1",
		Mission:  mission,
		Pipeline: pipeline,
		At:       baseTime,
		Header:   header("evt-run"),
	})
	if err != nil {
		t.Fatalf("сборка запуска: %v", err)
	}
	return run
}

func TestPipelineNewRejectsUnregisteredBlock(t *testing.T) {
	registry := testRegistry(t)
	_, err := NewPipeline("pipe-1", "tenant-1", 1, []StepSpec{spec("s1", "ghost")}, registry)
	if !errors.Is(err, ErrUnregisteredBlock) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnregisteredBlock", err)
	}
}

func TestPipelinePublishedIsImmutable(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"))

	if err := pipeline.Publish(at(1), header("evt-publish-2")); !errors.Is(err, ErrPipelineAlreadyPublished) {
		t.Fatalf("повторная публикация: ошибка = %v, ожидалась ErrPipelineAlreadyPublished", err)
	}
	if !pipeline.IsPublished() || pipeline.Version() != 1 {
		t.Fatalf("опубликованная версия изменилась: published=%v version=%d", pipeline.IsPublished(), pipeline.Version())
	}
}

func TestPipelineReviseCreatesNewVersion(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"))

	revised, err := pipeline.Revise(
		[]StepSpec{spec("s1", "search"), spec("s2", "classify")},
		registry,
	)
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if revised.Version() != 2 {
		t.Fatalf("версия новой версии = %d, ожидалась 2", revised.Version())
	}
	if revised.IsPublished() {
		t.Fatal("новая версия должна быть черновиком до публикации")
	}
	// Исходная версия не изменилась: неизменяемость (инвариант спеки).
	if pipeline.Version() != 1 || len(pipeline.Steps()) != 1 {
		t.Fatalf("исходная версия изменилась: version=%d steps=%d", pipeline.Version(), len(pipeline.Steps()))
	}
}

func TestPipelineReviseRejectsUnregisteredBlock(t *testing.T) {
	registry := testRegistry(t)
	pipeline := publishedPipeline(t, registry, spec("s1", "search"))

	if _, err := pipeline.Revise([]StepSpec{spec("s2", "ghost")}, registry); !errors.Is(err, ErrUnregisteredBlock) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnregisteredBlock", err)
	}
}

func TestPipelinePublishEmitsEvent(t *testing.T) {
	registry := testRegistry(t)
	pipeline, err := NewPipeline("pipe-1", "tenant-1", 1, []StepSpec{spec("s1", "search")}, registry)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if events := pipeline.DomainEvents(); len(events) != 0 {
		t.Fatalf("черновик не должен публиковать события: %d", len(events))
	}
	if err := pipeline.Publish(baseTime, header("evt-publish")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	events := pipeline.DomainEvents()
	if len(events) != 1 || events[0].Name() != EventPipelinePublished {
		t.Fatalf("события публикации = %v", events)
	}
}
