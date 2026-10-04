package analytics_test

import (
	"context"
	"io"
	"testing"
	"time"

	analytics "lead-gen/backend/contexts/analytics"
	"lead-gen/backend/contexts/analytics/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/backend/platform/observability/observ"
)

// TestProjectionWarmStartAndWriteThrough проверяет проводку хранилища проекции:
// проекция восстанавливается из хранилища при сборке (тёплый старт), а применение
// события записывает обновлённый снимок обратно.
func TestProjectionWarmStartAndWriteThrough(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	store := inmemory.New()
	if err := store.SaveCompany(ctx, readmodel.CompanyRecord{
		TenantID:  "t1",
		CompanyID: "c1",
		Stage:     readmodel.StageWarming,
		FoundAt:   base,
		HasSent:   true,
	}); err != nil {
		t.Fatalf("подготовка хранилища: %v", err)
	}

	slice, err := analytics.Build(analytics.Config{
		ProjectionStore: store,
		Logger:          observ.NewLoggerWithWriter(io.Discard),
		Metrics:         observ.NewMetrics(),
		Tracer:          observ.NewTracer(analytics.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка: %v", err)
	}

	// Тёплый старт: проекция восстановлена из хранилища до старта ролей.
	restored, ok := slice.ReadModel.Record("t1", "c1")
	if !ok || restored.Stage != readmodel.StageWarming || !restored.HasSent {
		t.Fatalf("проекция не восстановлена из хранилища: %+v (ok=%v)", restored, ok)
	}

	// Сквозная запись: применение события сохраняет обновлённый снимок.
	if err := slice.Events.Handle(ctx, command.ApplyEvent{
		TenantID:   "t1",
		CompanyID:  "c1",
		Kind:       string(readmodel.KindDeliveryEvent),
		Outcome:    "bounced",
		OccurredAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("применение события: %v", err)
	}

	records, err := store.LoadCompanies(ctx)
	if err != nil {
		t.Fatalf("чтение хранилища: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("в хранилище %d записей, ожидалась одна", len(records))
	}
	if records[0].Stage != readmodel.StageCold || !records[0].HasSent {
		t.Fatalf("сохранённый снимок не отражает событие: %+v", records[0])
	}
}
