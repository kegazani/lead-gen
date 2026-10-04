package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// RenderJobRepositoryFactory создаёт репозиторий для одного прогона suite.
type RenderJobRepositoryFactory func(t *testing.T) ports.RenderJobRepository

// RunRenderJobRepositoryContract проверяет, что реализация RenderJobRepository
// соблюдает контракт порта: сохраняет и загружает задание с артефактом,
// различает отсутствие, отвергает устаревшее сохранение конфликтом версий и
// выдаёт очередь незавершённых заданий в воспроизводимом порядке.
func RunRenderJobRepositoryContract(t *testing.T, newRepository RenderJobRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		job := mustJob(t, "job-roundtrip", domain.RenderStateRequested)
		if err := repository.Save(ctx, job, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, job.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertJob(t, loaded, job)
	})

	t.Run("артефакт переживает запись", func(t *testing.T) {
		repository := newRepository(t)
		job := mustJob(t, "job-rendered", domain.RenderStateRendered)
		if err := repository.Save(ctx, job, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, job.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertJob(t, loaded, job)
		if _, ok := loaded.Artifact(); !ok {
			t.Fatal("артефакт не восстановлен")
		}
	})

	t.Run("отсутствие задания", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, "job-missing")
		if err == nil {
			t.Fatal("FindByID отсутствующего задания вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrRenderJobNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrRenderJobNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующего задания ожидался nil, получено %v", loaded)
		}
	})

	t.Run("оптимистичная блокировка", func(t *testing.T) {
		repository := newRepository(t)
		job := mustJob(t, "job-lock", domain.RenderStateRequested)
		if err := repository.Save(ctx, job, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}

		stale, err := repository.FindByID(ctx, job.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		fresh, err := repository.FindByID(ctx, job.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}

		expected := fresh.Version()
		if err := fresh.Start(domain.RenderTransitionInput{Actor: "worker", At: at(1)}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := repository.Save(ctx, fresh, expected); err != nil {
			t.Fatalf("Save свежего задания: %v", err)
		}

		if err := repository.Save(ctx, stale, expected); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("устаревшее сохранение: ошибка = %v, ожидалась ErrVersionConflict", err)
		}

		reloaded, err := repository.FindByID(ctx, job.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if reloaded.State() != domain.RenderStateRendering {
			t.Fatalf("конфликтное сохранение изменило состояние: %q", reloaded.State())
		}
		if reloaded.Version() != expected+1 {
			t.Fatalf("версия после изменения = %d, ожидалась %d", reloaded.Version(), expected+1)
		}
	})

	t.Run("создание с ненулевой ожидаемой версией", func(t *testing.T) {
		repository := newRepository(t)
		job := mustJob(t, "job-new-version", domain.RenderStateRequested)
		if err := repository.Save(ctx, job, 5); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("ошибка = %v, ожидалась ErrVersionConflict", err)
		}
		if _, err := repository.FindByID(ctx, job.ID()); !errors.Is(err, ports.ErrRenderJobNotFound) {
			t.Fatalf("отвергнутое создание оставило задание: ошибка = %v", err)
		}
	})

	t.Run("очередь незавершённых заданий упорядочена", func(t *testing.T) {
		repository := newRepository(t)
		jobs := []*domain.RenderJob{
			mustJob(t, "job-1", domain.RenderStateRequested),
			mustJob(t, "job-2", domain.RenderStateRendering),
			mustJob(t, "job-3", domain.RenderStateRendered),
			mustJob(t, "job-4", domain.RenderStateFailed),
		}
		for _, job := range jobs {
			if err := repository.Save(ctx, job, 0); err != nil {
				t.Fatalf("Save %s: %v", job.ID(), err)
			}
		}

		pending, err := repository.ListPending(ctx, 0)
		if err != nil {
			t.Fatalf("ListPending: %v", err)
		}
		if len(pending) != 2 {
			t.Fatalf("ожидалось 2 незавершённых задания, получено %d", len(pending))
		}
		if pending[0].ID() != "job-1" || pending[1].ID() != "job-2" {
			t.Fatalf("порядок заданий не воспроизводим: %s, %s", pending[0].ID(), pending[1].ID())
		}

		limited, err := repository.ListPending(ctx, 1)
		if err != nil {
			t.Fatalf("ListPending(1): %v", err)
		}
		if len(limited) != 1 || limited[0].ID() != "job-1" {
			t.Fatalf("ограничение порции не соблюдено: %v", renderJobIDs(limited))
		}
	})
}

// renderJobIDs возвращает идентификаторы заданий для диагностики.
func renderJobIDs(jobs []*domain.RenderJob) []domain.RenderJobID {
	ids := make([]domain.RenderJobID, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID())
	}
	return ids
}
