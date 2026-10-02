package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/publishing/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

var at = time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

func newPublication(t *testing.T, id, key, platform string) *domain.Publication {
	t.Helper()
	target, err := domain.NewPublicationTarget(platform, "acct")
	if err != nil {
		t.Fatalf("NewPublicationTarget: %v", err)
	}
	material, err := domain.NewMaterial(target, "Заголовок", "текст")
	if err != nil {
		t.Fatalf("NewMaterial: %v", err)
	}
	publication, err := domain.NewPublication(domain.NewPublicationParams{
		ID:             domain.PublicationID(id),
		TenantID:       "tenant-1",
		Digest:         domain.DigestRef{ID: "digest-1", CommunityID: "community-1", Period: "2026-W10"},
		Target:         target,
		Material:       material,
		IdempotencyKey: domain.IdempotencyKey(key),
		At:             at,
	})
	if err != nil {
		t.Fatalf("NewPublication: %v", err)
	}
	return publication
}

func TestSaveAndFind(t *testing.T) {
	ctx := context.Background()
	repo := persistence.New()
	publication := newPublication(t, "pub-1", "key-1", "linkedin")

	if err := repo.Save(ctx, publication, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	restored, err := repo.FindByID(ctx, "pub-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if restored.IdempotencyKey() != "key-1" || restored.Status() != domain.StatusDraft {
		t.Fatalf("восстановлено не то: %+v", restored)
	}

	byKey, err := repo.FindByIdempotencyKey(ctx, "key-1")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey: %v", err)
	}
	if byKey.ID() != "pub-1" {
		t.Fatalf("найдено %s, ожидалась pub-1", byKey.ID())
	}
}

func TestFindMissing(t *testing.T) {
	ctx := context.Background()
	repo := persistence.New()
	if _, err := repo.FindByID(ctx, "nope"); !errors.Is(err, ports.ErrPublicationNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrPublicationNotFound", err)
	}
	if _, err := repo.FindByIdempotencyKey(ctx, "nope"); !errors.Is(err, ports.ErrPublicationNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrPublicationNotFound", err)
	}
}

func TestSaveVersionConflict(t *testing.T) {
	ctx := context.Background()
	repo := persistence.New()
	publication := newPublication(t, "pub-1", "key-1", "linkedin")
	if err := repo.Save(ctx, publication, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Save(ctx, publication, 7); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("ошибка = %v, ожидалась ErrVersionConflict", err)
	}
}

func TestSnapshotIsolatedFromStore(t *testing.T) {
	ctx := context.Background()
	repo := persistence.New()
	publication := newPublication(t, "pub-1", "key-1", "linkedin")
	if err := repo.Save(ctx, publication, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Изменение вызывающим агрегата после сохранения не меняет хранилище.
	if err := publication.Approve("operator-1", at); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	stored, err := repo.FindByID(ctx, "pub-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.Status() != domain.StatusDraft {
		t.Fatalf("хранилище изменилось вслед за агрегатом: %q", stored.Status())
	}
}

func TestListByStatus(t *testing.T) {
	ctx := context.Background()
	repo := persistence.New()
	for _, id := range []string{"pub-b", "pub-a"} {
		if err := repo.Save(ctx, newPublication(t, id, "key-"+id, "linkedin"), 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	drafts, err := repo.ListByStatus(ctx, domain.StatusDraft)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(drafts) != 2 || drafts[0].ID() != "pub-a" || drafts[1].ID() != "pub-b" {
		t.Fatalf("порядок/состав очереди неверен: %+v", drafts)
	}
}
