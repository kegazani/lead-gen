package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// PipelineRepositoryFactory создаёт репозиторий пайплайнов с пустым состоянием для
// одного прогона suite. Для общей PostgreSQL-базы фабрика отвечает за изоляцию
// прогонов.
type PipelineRepositoryFactory func(t *testing.T) ports.PipelineRepository

// RunRunRepositoryContract проверяет контракт репозитория запусков: сохранение и
// загрузку, тенант-скоуп, оптимистичную блокировку, выборку по статусу и подсчёт
// активных запусков.
func RunRunRepositoryContract(t *testing.T, newRepository RunRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		run := NewRun(t, "run-roundtrip", PublishedPipeline(t, "tenant-1", 1))
		if err := repository.Save(ctx, run, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, run.ID(), "tenant-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		AssertRun(t, loaded, run)
	})

	t.Run("отсутствие запуска", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, "run-missing", "tenant-1")
		if !errors.Is(err, ports.ErrRunNotFound) {
			t.Fatalf("ошибка = %v, ожидалась ErrRunNotFound", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующего запуска ожидался nil, получено %v", loaded)
		}
	})

	t.Run("изоляция по тенанту", func(t *testing.T) {
		repository := newRepository(t)
		run := NewRun(t, "run-1", PublishedPipeline(t, "tenant-1", 1))
		if err := repository.Save(ctx, run, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if _, err := repository.FindByID(ctx, run.ID(), "tenant-2"); !errors.Is(err, ports.ErrRunNotFound) {
			t.Fatalf("чтение чужого тенанта: ошибка = %v, ожидалась ErrRunNotFound", err)
		}
	})

	t.Run("оптимистичная блокировка", func(t *testing.T) {
		repository := newRepository(t)
		run := NewRun(t, "run-lock", PublishedPipeline(t, "tenant-1", 1))
		if err := repository.Save(ctx, run, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		fresh, err := repository.FindByID(ctx, run.ID(), "tenant-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if err := fresh.Start(baseTime, header("evt-start")); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := repository.Save(ctx, fresh, 0); err != nil {
			t.Fatalf("Save свежей ревизии: %v", err)
		}
		if err := repository.Save(ctx, fresh, 0); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("устаревшее сохранение: ошибка = %v, ожидалась ErrVersionConflict", err)
		}
	})

	t.Run("выборка по статусу упорядочена", func(t *testing.T) {
		repository := newRepository(t)
		for _, id := range []domain.RunID{"run-b", "run-a"} {
			if err := repository.Save(ctx, NewRun(t, id, PublishedPipeline(t, "tenant-1", 1)), 0); err != nil {
				t.Fatalf("Save %s: %v", id, err)
			}
		}
		queued, err := repository.ListByStatus(ctx, domain.StatusQueued, "tenant-1", 0)
		if err != nil {
			t.Fatalf("ListByStatus: %v", err)
		}
		if len(queued) != 2 || queued[0].ID() != "run-a" || queued[1].ID() != "run-b" {
			t.Fatalf("queued = %v, ожидались run-a, run-b по порядку", runIDs(queued))
		}
		if _, err := repository.ListByStatus(ctx, domain.StatusQueued, "tenant-1", 1); err != nil {
			t.Fatalf("ListByStatus с пределом: %v", err)
		}
		limited, _ := repository.ListByStatus(ctx, domain.StatusQueued, "tenant-1", 1)
		if len(limited) != 1 {
			t.Fatalf("предел выборки не соблюдён: %d", len(limited))
		}
	})

	t.Run("лента: тенант-скоуп, статус и порядок", func(t *testing.T) {
		repository := newRepository(t)
		older := NewRun(t, "run-a", PublishedPipeline(t, "tenant-1", 1))
		if err := repository.Save(ctx, older, 0); err != nil {
			t.Fatalf("Save run-a: %v", err)
		}
		newer := NewRun(t, "run-b", PublishedPipeline(t, "tenant-1", 1))
		if err := newer.Start(at(5), header("evt-start-b")); err != nil {
			t.Fatalf("Start run-b: %v", err)
		}
		if err := repository.Save(ctx, newer, 0); err != nil {
			t.Fatalf("Save run-b: %v", err)
		}
		foreign := NewRun(t, "run-c", PublishedPipeline(t, "tenant-2", 1))
		if err := repository.Save(ctx, foreign, 0); err != nil {
			t.Fatalf("Save run-c: %v", err)
		}

		all, err := repository.List(ctx, ports.RunFilter{Tenant: "tenant-1"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 2 || all[0].ID() != "run-b" || all[1].ID() != "run-a" {
			t.Fatalf("лента = %v, ожидались run-b, run-a (новые сверху)", runIDs(all))
		}

		running, err := repository.List(ctx, ports.RunFilter{Tenant: "tenant-1", Status: domain.StatusRunning, Limit: 1})
		if err != nil {
			t.Fatalf("List со статусом: %v", err)
		}
		if len(running) != 1 || running[0].ID() != "run-b" {
			t.Fatalf("фильтр по статусу = %v, ожидался run-b", runIDs(running))
		}

		other, err := repository.List(ctx, ports.RunFilter{Tenant: "tenant-2"})
		if err != nil {
			t.Fatalf("List чужого тенанта: %v", err)
		}
		if len(other) != 1 || other[0].ID() != "run-c" {
			t.Fatalf("изоляция тенанта нарушена: %v", runIDs(other))
		}

		if _, err := repository.List(ctx, ports.RunFilter{}); !errors.Is(err, ports.ErrMissingTenant) {
			t.Fatalf("выборка без тенанта: ошибка = %v, ожидалась ErrMissingTenant", err)
		}
	})

	t.Run("подсчёт активных запусков", func(t *testing.T) {
		repository := newRepository(t)
		run := NewRun(t, "run-active", PublishedPipeline(t, "tenant-1", 1))
		if err := repository.Save(ctx, run, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		// Запуск в очереди слот одновременной обработки не занимает.
		if count, err := repository.CountActive(ctx, "tenant-1"); err != nil || count != 0 {
			t.Fatalf("активных в очереди = %d (err=%v), ожидался 0", count, err)
		}

		running, err := repository.FindByID(ctx, run.ID(), "tenant-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		revision := running.Revision()
		if err := running.Start(baseTime, header("evt-start")); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := repository.Save(ctx, running, revision); err != nil {
			t.Fatalf("Save после старта: %v", err)
		}
		if count, err := repository.CountActive(ctx, "tenant-1"); err != nil || count != 1 {
			t.Fatalf("активных после старта = %d (err=%v), ожидался 1", count, err)
		}

		stopped, err := repository.FindByID(ctx, run.ID(), "tenant-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		revision = stopped.Revision()
		if err := stopped.Stop("actor-1", "стоп", at(1), header("evt-stop")); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if err := repository.Save(ctx, stopped, revision); err != nil {
			t.Fatalf("Save после остановки: %v", err)
		}
		if count, err := repository.CountActive(ctx, "tenant-1"); err != nil || count != 0 {
			t.Fatalf("активных после остановки = %d (err=%v), ожидался 0", count, err)
		}
	})
}

// RunPipelineRepositoryContract проверяет контракт репозитория пайплайнов:
// сохранение и загрузку версии, отсутствие, последнюю опубликованную версию и
// оптимистичную блокировку.
func RunPipelineRepositoryContract(t *testing.T, newRepository PipelineRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		pipeline := PublishedPipeline(t, "tenant-1", 1)
		if err := repository.Save(ctx, pipeline, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, pipeline.ID(), pipeline.Version(), "tenant-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		AssertPipeline(t, loaded, pipeline)
	})

	t.Run("отсутствие версии", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, "pipe-missing", 1, "tenant-1")
		if !errors.Is(err, ports.ErrPipelineNotFound) {
			t.Fatalf("ошибка = %v, ожидалась ErrPipelineNotFound", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующей версии ожидался nil, получено %v", loaded)
		}
	})

	t.Run("последняя опубликованная версия", func(t *testing.T) {
		repository := newRepository(t)
		for _, version := range []int{1, 2} {
			if err := repository.Save(ctx, PublishedPipeline(t, "tenant-1", version), 0); err != nil {
				t.Fatalf("Save v%d: %v", version, err)
			}
		}
		latest, err := repository.LatestPublished(ctx, "pipe-1", "tenant-1")
		if err != nil {
			t.Fatalf("LatestPublished: %v", err)
		}
		if latest != 2 {
			t.Fatalf("последняя версия = %d, ожидалась 2", latest)
		}
		if _, err := repository.LatestPublished(ctx, "pipe-1", "tenant-2"); !errors.Is(err, ports.ErrPipelineNotFound) {
			t.Fatalf("чужой тенант: ошибка = %v, ожидалась ErrPipelineNotFound", err)
		}
	})

	t.Run("оптимистичная блокировка", func(t *testing.T) {
		repository := newRepository(t)
		pipeline := PublishedPipeline(t, "tenant-1", 1)
		if err := repository.Save(ctx, pipeline, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, pipeline.ID(), pipeline.Version(), "tenant-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if err := repository.Save(ctx, loaded, loaded.Revision()); err != nil {
			t.Fatalf("Save актуальной ревизии: %v", err)
		}
		if err := repository.Save(ctx, pipeline, 0); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("устаревшее сохранение: ошибка = %v, ожидалась ErrVersionConflict", err)
		}
	})
}

// RunRepositoryFactory создаёт репозиторий запусков для одного прогона suite.
type RunRepositoryFactory func(t *testing.T) ports.RunRepository

// runIDs возвращает идентификаторы запусков для диагностики.
func runIDs(runs []*domain.AgentRun) []domain.RunID {
	ids := make([]domain.RunID, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID())
	}
	return ids
}
