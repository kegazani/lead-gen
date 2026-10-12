// Package contracttest содержит общие contract suite портов контекста
// agent-orchestration: репозитория пайплайнов и репозитория запусков. Suite
// запускается против любой реализации через фабрику и потому не зависит от
// адаптера: in-memory и PostgreSQL проверяются одним и тем же набором проверок
// (design.md, Реш. 15).
//
// Пакет предназначен для использования из тестов адаптеров и импортирует только
// сам ports и его domain.
package contracttest

import (
	"testing"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
)

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC)

// at возвращает момент времени, сдвинутый на minutes минут от baseTime.
func at(minutes int) time.Time { return baseTime.Add(time.Duration(minutes) * time.Minute) }

// header собирает конверт события с заданным event_id.
func header(eventID string) domain.EventHeader {
	return domain.EventHeader{EventID: domain.EventID(eventID), CorrelationID: "corr-1"}
}

// Registry собирает реестр блоков, покрывающий тестовый пайплайн.
func Registry(t *testing.T) *domain.BlockRegistry {
	t.Helper()
	specs := []struct {
		ref       domain.BlockRef
		contract  string
		irr, mand bool
	}{
		{"search", "lead-search", false, true},
		{"classify", "lead-relevance", false, true},
		{"send", "delivery-command", true, true},
	}
	blocks := make([]domain.Block, 0, len(specs))
	for _, spec := range specs {
		block, err := domain.NewBlock(spec.ref, string(spec.ref), spec.contract, spec.irr, spec.mand)
		if err != nil {
			t.Fatalf("сборка блока %s: %v", spec.ref, err)
		}
		blocks = append(blocks, block)
	}
	registry, err := domain.NewBlockRegistry(blocks...)
	if err != nil {
		t.Fatalf("сборка реестра: %v", err)
	}
	return registry
}

// PublishedPipeline собирает опубликованную версию пайплайна тенанта tenant.
func PublishedPipeline(t *testing.T, tenant domain.TenantID, version int) *domain.AgentPipeline {
	t.Helper()
	config, err := domain.NewStepConfig(nil, 0)
	if err != nil {
		t.Fatalf("NewStepConfig: %v", err)
	}
	specs := []domain.StepSpec{
		{ID: "search", Ref: "search", Config: config},
		{ID: "classify", Ref: "classify", Config: config},
	}
	pipeline, err := domain.NewPipeline("pipe-1", tenant, version, specs, Registry(t))
	if err != nil {
		t.Fatalf("сборка пайплайна: %v", err)
	}
	if err := pipeline.Publish(baseTime, header("evt-publish")); err != nil {
		t.Fatalf("публикация пайплайна: %v", err)
	}
	return pipeline
}

// NewRun собирает запуск по опубликованному пайплайну.
func NewRun(t *testing.T, id domain.RunID, pipeline *domain.AgentPipeline) *domain.AgentRun {
	t.Helper()
	mission, err := domain.NewAgentMission("mission-1", "company-1", pipeline.TenantID(), "ищем IT-компании")
	if err != nil {
		t.Fatalf("сборка миссии: %v", err)
	}
	run, err := domain.NewRun(domain.NewRunParams{
		ID:       id,
		Mission:  mission,
		Pipeline: pipeline,
		At:       baseTime,
		Header:   header("evt-" + string(id)),
	})
	if err != nil {
		t.Fatalf("сборка запуска: %v", err)
	}
	return run
}

// AssertPipeline сверяет загруженную версию пайплайна с ожидаемой по наблюдаемому
// состоянию.
func AssertPipeline(t *testing.T, got, want *domain.AgentPipeline) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.Version() != want.Version() {
		t.Fatalf("Version: получено %d, ожидалось %d", got.Version(), want.Version())
	}
	if got.TenantID() != want.TenantID() {
		t.Fatalf("TenantID: получено %q, ожидалось %q", got.TenantID(), want.TenantID())
	}
	if got.IsPublished() != want.IsPublished() {
		t.Fatalf("IsPublished: получено %v, ожидалось %v", got.IsPublished(), want.IsPublished())
	}
	if len(got.Steps()) != len(want.Steps()) {
		t.Fatalf("число шагов: получено %d, ожидалось %d", len(got.Steps()), len(want.Steps()))
	}
	for i, step := range want.Steps() {
		if got.Steps()[i].ID() != step.ID() || got.Steps()[i].Ref() != step.Ref() {
			t.Fatalf("шаг %d: получено %s/%s, ожидалось %s/%s", i, got.Steps()[i].ID(), got.Steps()[i].Ref(), step.ID(), step.Ref())
		}
	}
}

// AssertRun сверяет загруженный запуск с ожидаемым по наблюдаемому состоянию.
func AssertRun(t *testing.T, got, want *domain.AgentRun) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.TenantID() != want.TenantID() {
		t.Fatalf("TenantID: получено %q, ожидалось %q", got.TenantID(), want.TenantID())
	}
	if got.Status() != want.Status() {
		t.Fatalf("Status: получено %q, ожидалось %q", got.Status(), want.Status())
	}
	if got.PipelineVersion() != want.PipelineVersion() {
		t.Fatalf("PipelineVersion: получено %d, ожидалось %d", got.PipelineVersion(), want.PipelineVersion())
	}
	if len(got.Steps()) != len(want.Steps()) {
		t.Fatalf("число шагов: получено %d, ожидалось %d", len(got.Steps()), len(want.Steps()))
	}
}
