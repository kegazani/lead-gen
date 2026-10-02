package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

var testMoment = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newSync(t *testing.T) *domain.CRMSync {
	t.Helper()
	external, err := domain.NewExternalVersion(domain.ExternalDealRef("crm-1"), 1)
	if err != nil {
		t.Fatalf("NewExternalVersion: %v", err)
	}
	sync, err := domain.NewCRMSync(domain.NewSyncParams{
		ID:       domain.CRMSyncID("crm-sync:deal-1"),
		TenantID: domain.TenantID("tenant-1"),
		DealID:   domain.DealID("deal-1"),
		External: external,
		At:       testMoment,
	})
	if err != nil {
		t.Fatalf("NewCRMSync: %v", err)
	}
	return sync
}

func TestSyncRepositorySaveFind(t *testing.T) {
	repo := New()
	sync := newSync(t)
	if err := repo.Save(context.Background(), sync); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := repo.FindByDeal(context.Background(), "deal-1")
	if err != nil {
		t.Fatalf("FindByDeal: %v", err)
	}
	if loaded.Status() != domain.SyncSynced || loaded.Version().Revision() != 1 {
		t.Fatalf("загружено %+v", loaded.Snapshot())
	}

	if _, err := repo.FindByDeal(context.Background(), "deal-missing"); !errors.Is(err, ports.ErrCRMSyncNotFound) {
		t.Fatalf("отсутствующая синхронизация: %v", err)
	}
}

func TestSyncRepositoryCloneReplace(t *testing.T) {
	repo := New()
	if err := repo.Save(context.Background(), newSync(t)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	clone := repo.Clone()
	if err := clone.Save(context.Background(), mustSync(t, 2)); err != nil {
		t.Fatalf("Save в копию: %v", err)
	}
	// До commit основное состояние не изменилось.
	loaded, err := repo.FindByDeal(context.Background(), "deal-1")
	if err != nil {
		t.Fatalf("FindByDeal: %v", err)
	}
	if loaded.Version().Revision() != 1 {
		t.Fatalf("копия изменила основное состояние: %d", loaded.Version().Revision())
	}

	repo.ReplaceWith(clone)
	after, err := repo.FindByDeal(context.Background(), "deal-1")
	if err != nil {
		t.Fatalf("FindByDeal после ReplaceWith: %v", err)
	}
	if after.Version().Revision() != 2 {
		t.Fatalf("commit не перенёс состояние: %d", after.Version().Revision())
	}
}

func mustSync(t *testing.T, revision int64) *domain.CRMSync {
	t.Helper()
	external, err := domain.NewExternalVersion(domain.ExternalDealRef("crm-1"), revision)
	if err != nil {
		t.Fatalf("NewExternalVersion: %v", err)
	}
	sync, err := domain.NewCRMSync(domain.NewSyncParams{
		ID:       domain.CRMSyncID("crm-sync:deal-1"),
		TenantID: domain.TenantID("tenant-1"),
		DealID:   domain.DealID("deal-1"),
		External: external,
		At:       testMoment,
	})
	if err != nil {
		t.Fatalf("NewCRMSync: %v", err)
	}
	return sync
}
