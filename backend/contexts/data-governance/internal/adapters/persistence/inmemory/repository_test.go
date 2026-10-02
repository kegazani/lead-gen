package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

var (
	ctx      = context.Background()
	baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

// newBasis собирает действующее основание с одним днём окна действия.
func newBasis(t *testing.T, id domain.BasisID, tenant domain.TenantID) *domain.ProcessingBasis {
	t.Helper()
	purpose, err := domain.NewProcessingPurpose("outreach", domain.CategoryGreen)
	if err != nil {
		t.Fatalf("цель: %v", err)
	}
	retention, err := domain.NewRetentionPeriod(90)
	if err != nil {
		t.Fatalf("срок: %v", err)
	}
	basis, err := domain.NewProcessingBasis(domain.NewBasisParams{
		ID: id, TenantID: tenant, Purpose: purpose, Retention: retention,
		ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 1),
		Actor: "actor-1", At: baseTime, EventID: "evt-1",
	})
	if err != nil {
		t.Fatalf("основание: %v", err)
	}
	return basis
}

// TestSaveAndLoad проверяет запись и чтение с восстановлением инвариантов.
func TestSaveAndLoad(t *testing.T) {
	t.Parallel()

	repo := inmemory.New()
	basis := newBasis(t, "basis-1", "tenant-1")
	if err := repo.Save(ctx, basis, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := repo.FindByID(ctx, "basis-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.TenantID() != "tenant-1" || loaded.Version() != 1 {
		t.Fatalf("загруженное основание = %+v", loaded)
	}
}

// TestSnapshotIsolation проверяет, что сохранённый агрегат изолирован от
// последующих правок вызывающего.
func TestSnapshotIsolation(t *testing.T) {
	t.Parallel()

	repo := inmemory.New()
	basis := newBasis(t, "basis-1", "tenant-1")
	if err := repo.Save(ctx, basis, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := basis.Revoke(domain.RevokeParams{
		Actor: "actor-1", Reason: "отзыв", At: baseTime.Add(time.Hour), EventID: "evt-2",
	}); err != nil {
		t.Fatalf("отзыв: %v", err)
	}
	loaded, err := repo.FindByID(ctx, "basis-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.Status() != domain.BasisActive {
		t.Fatalf("сохранённое состояние изменилось: %q", loaded.Status())
	}
}

// TestVersionConflict проверяет оптимистичную блокировку.
func TestVersionConflict(t *testing.T) {
	t.Parallel()

	repo := inmemory.New()
	basis := newBasis(t, "basis-1", "tenant-1")
	if err := repo.Save(ctx, basis, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := basis.Revoke(domain.RevokeParams{
		Actor: "actor-1", Reason: "отзыв", At: baseTime.Add(time.Hour), EventID: "evt-2",
	}); err != nil {
		t.Fatalf("отзыв: %v", err)
	}
	if err := repo.Save(ctx, basis, 0); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("устаревшая версия: ошибка = %v, ожидалась ErrVersionConflict", err)
	}
	if err := repo.Save(ctx, basis, 1); err != nil {
		t.Fatalf("актуальная версия: %v", err)
	}
}

// TestFindByTenantAndNotFound проверяет фильтр по тенанту и ошибку отсутствия.
func TestFindByTenantAndNotFound(t *testing.T) {
	t.Parallel()

	repo := inmemory.New()
	if err := repo.Save(ctx, newBasis(t, "basis-1", "tenant-1"), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Save(ctx, newBasis(t, "basis-2", "tenant-2"), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	found, err := repo.FindByTenant(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("FindByTenant: %v", err)
	}
	if len(found) != 1 || found[0].ID() != "basis-1" {
		t.Fatalf("основания тенанта = %+v", found)
	}

	if _, err := repo.FindByID(ctx, "ghost"); !errors.Is(err, ports.ErrBasisNotFound) {
		t.Fatalf("отсутствующее основание: ошибка = %v", err)
	}
}

// TestListExpired проверяет отбор оснований с истёкшим окном действия.
func TestListExpired(t *testing.T) {
	t.Parallel()

	repo := inmemory.New()
	if err := repo.Save(ctx, newBasis(t, "basis-1", "tenant-1"), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := repo.ListExpired(ctx, baseTime); err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	expired, err := repo.ListExpired(ctx, baseTime.AddDate(0, 0, 2))
	if err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	if len(expired) != 1 || expired[0].ID() != "basis-1" {
		t.Fatalf("истёкшие = %+v", expired)
	}
}
