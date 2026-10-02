package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

func newPackage(t *testing.T) *domain.ContentPackage {
	t.Helper()
	return newPackageWithID(t, "asset-1", "ver-1")
}

func newPackageWithID(t *testing.T, assetID domain.ContentAssetID, versionID domain.ContentVersionID) *domain.ContentPackage {
	t.Helper()
	locale, err := domain.NewContentLocale("en", "US")
	if err != nil {
		t.Fatalf("NewContentLocale: %v", err)
	}
	pkg, err := domain.NewContentPackage(domain.NewContentPackageParams{
		ID:        assetID,
		TenantID:  "tenant-1",
		Locale:    locale,
		VersionID: versionID,
		Body:      "Text.",
		Actor:     "actor-1",
		At:        time.Unix(1000, 0).UTC(),
		EventID:   "evt-1",
	})
	if err != nil {
		t.Fatalf("NewContentPackage: %v", err)
	}
	return pkg
}

func newJob(t *testing.T, id domain.RenderJobID, state domain.RenderState) *domain.RenderJob {
	t.Helper()
	job, err := domain.NewRenderJob(domain.NewRenderJobParams{
		ID:         id,
		TenantID:   "tenant-1",
		AssetID:    "asset-1",
		VersionID:  "ver-1",
		TemplateID: "tmpl-1",
		Format:     domain.RenderFormatLandingPage,
	})
	if err != nil {
		t.Fatalf("NewRenderJob: %v", err)
	}
	if state == domain.RenderStateRendering {
		if err := job.Start(domain.RenderTransitionInput{Actor: "worker", At: time.Unix(2, 0).UTC()}); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	if state == domain.RenderStateRendered {
		if err := job.Start(domain.RenderTransitionInput{Actor: "worker", At: time.Unix(2, 0).UTC()}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		section, _ := domain.NewLandingSection("H", "B.")
		landing, _ := domain.NewLandingContent("lc-1", "ver-1", "T", "H", "I", []domain.LandingSection{section})
		if err := job.Complete(domain.RenderTransitionInput{Actor: "worker", At: time.Unix(3, 0).UTC(), EventID: "evt-2"}, domain.NewLandingArtifact("asset-1", landing)); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	return job
}

func TestContentRepositorySaveAndFind(t *testing.T) {
	ctx := context.Background()
	store := New()
	repo := store.Content()

	pkg := newPackage(t)
	if err := repo.Save(ctx, pkg, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := repo.FindByID(ctx, "asset-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.ID() != "asset-1" || loaded.Version() != 1 {
		t.Fatalf("загружен не тот пакет: %+v", loaded)
	}

	// Оптимистичная блокировка: неверная ожидаемая версия отвергается.
	if err := repo.Save(ctx, pkg, 99); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("ожидалась ErrVersionConflict, получено %v", err)
	}
	if _, err := repo.FindByID(ctx, "missing"); !errors.Is(err, ports.ErrContentPackageNotFound) {
		t.Fatalf("ожидалась ErrContentPackageNotFound, получено %v", err)
	}
}

func TestRenderJobRepositoryListPending(t *testing.T) {
	ctx := context.Background()
	store := New()
	repo := store.RenderJobs()

	jobs := []*domain.RenderJob{
		newJob(t, "job-1", domain.RenderStateRequested),
		newJob(t, "job-2", domain.RenderStateRendering),
		newJob(t, "job-3", domain.RenderStateRendered),
	}
	for _, job := range jobs {
		if err := repo.Save(ctx, job, 0); err != nil {
			t.Fatalf("Save %s: %v", job.ID(), err)
		}
	}
	pending, err := repo.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("ожидалось 2 незавершённых задания, получено %d", len(pending))
	}
	if pending[0].ID() != "job-1" || pending[1].ID() != "job-2" {
		t.Fatalf("порядок заданий не воспроизводим: %s, %s", pending[0].ID(), pending[1].ID())
	}
	limited, err := repo.ListPending(ctx, 1)
	if err != nil {
		t.Fatalf("ListPending(1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("ограничение порции не соблюдено: %d", len(limited))
	}
}

func TestStoreCloneIsolation(t *testing.T) {
	ctx := context.Background()
	store := New()
	if err := store.Content().Save(ctx, newPackage(t), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clone := store.Clone()
	if err := clone.Content().Save(ctx, newPackageWithID(t, "asset-2", "ver-2"), 0); err != nil {
		t.Fatalf("Save в копию: %v", err)
	}

	if _, err := store.Content().FindByID(ctx, "asset-2"); !errors.Is(err, ports.ErrContentPackageNotFound) {
		t.Fatalf("копия протекла в основное хранилище: %v", err)
	}

	// Commit переносит состояние копии в основное хранилище.
	store.ReplaceWith(clone)
	if _, err := store.Content().FindByID(ctx, "asset-2"); err != nil {
		t.Fatalf("ReplaceWith не перенёс состояние: %v", err)
	}
}
