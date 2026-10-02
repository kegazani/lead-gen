package query_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/app/query"
	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

var (
	ctx      = context.Background()
	baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

// seedBasis создаёт и сохраняет действующее основание обработки.
func seedBasis(t *testing.T, repo *memoryRepo) *domain.ProcessingBasis {
	t.Helper()
	purpose, err := domain.NewProcessingPurpose("outreach", domain.CategoryRed)
	if err != nil {
		t.Fatalf("цель: %v", err)
	}
	retention, err := domain.NewRetentionPeriod(90)
	if err != nil {
		t.Fatalf("срок: %v", err)
	}
	basis, err := domain.NewProcessingBasis(domain.NewBasisParams{
		ID: "basis-1", TenantID: "tenant-1", Purpose: purpose, Retention: retention,
		ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 90),
		Actor: "actor-1", At: baseTime, EventID: "evt-1",
	})
	if err != nil {
		t.Fatalf("основание: %v", err)
	}
	if err := repo.Save(ctx, basis, 0); err != nil {
		t.Fatalf("сохранение: %v", err)
	}
	return basis
}

// TestCheckZonePolicy проверяет политику зон вместе с действием полномочия.
func TestCheckZonePolicy(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	seedBasis(t, repo)
	handler := query.NewCheckZoneHandler(repo, fixedClock{at: baseTime})

	view, err := handler.Handle(ctx, query.CheckZone{TenantID: "tenant-1", Purpose: "outreach", Category: domain.CategoryRed})
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if !view.Authorized || view.BasisID != "basis-1" {
		t.Fatalf("решение = %+v", view)
	}
	if view.Handling != domain.HandlingClientSide || view.SharedStorageAllowed {
		t.Fatalf("правило зоны = %+v", view)
	}

	other, err := handler.Handle(ctx, query.CheckZone{TenantID: "tenant-1", Purpose: "analytics", Category: domain.CategoryGreen})
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if other.Authorized {
		t.Fatalf("полномочие сработало для другой цели: %+v", other)
	}
}

// TestCheckAuthority проверяет снимок действующего полномочия.
func TestCheckAuthority(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	seedBasis(t, repo)
	handler := query.NewCheckAuthorityHandler(repo, fixedClock{at: baseTime})

	view, err := handler.Handle(ctx, query.CheckAuthority{TenantID: "tenant-1", Purpose: "outreach", Category: domain.CategoryRed})
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if !view.Authorized || view.RetentionDays != 90 || view.Status != domain.BasisActive {
		t.Fatalf("полномочие = %+v", view)
	}

	missing, err := handler.Handle(ctx, query.CheckAuthority{TenantID: "tenant-1", Purpose: "outreach", Category: domain.CategoryGreen})
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if missing.Authorized {
		t.Fatalf("полномочие найдено для чужой категории: %+v", missing)
	}
}

// TestCheckSuppression проверяет проверку подавления по всем основаниям тенанта.
func TestCheckSuppression(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	basis := seedBasis(t, repo)
	if _, err := basis.SuppressContact(domain.SuppressEntryParams{
		ID: "sup-1", Contact: "a@example.com", Reason: "отзыв согласия",
		Actor: "actor-1", At: baseTime, EventID: "evt-2",
	}); err != nil {
		t.Fatalf("стоп-лист: %v", err)
	}
	if err := repo.Save(ctx, basis, basis.Version()-1); err != nil {
		t.Fatalf("сохранение: %v", err)
	}

	handler := query.NewCheckSuppressionHandler(repo)
	view, err := handler.Handle(ctx, query.CheckSuppression{TenantID: "tenant-1", Contact: "a@example.com"})
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if !view.Suppressed || view.SuppressionID != "sup-1" || view.BasisID != "basis-1" {
		t.Fatalf("решение = %+v", view)
	}

	free, err := handler.Handle(ctx, query.CheckSuppression{TenantID: "tenant-1", Contact: "b@example.com"})
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if free.Suppressed {
		t.Fatalf("незапятнанный контакт подавлен: %+v", free)
	}

	if _, err := handler.Handle(ctx, query.CheckSuppression{TenantID: "  ", Contact: "a@example.com"}); !errors.Is(err, domain.ErrEmptyID) {
		t.Fatalf("пустой тенант: ошибка = %v", err)
	}
}

// TestGetBasisTenantIsolation проверяет изоляцию чтения по тенанту.
func TestGetBasisTenantIsolation(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	seedBasis(t, repo)
	handler := query.NewGetBasisHandler(repo)

	view, err := handler.Handle(ctx, query.GetBasis{BasisID: "basis-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if view.BasisID != "basis-1" || view.Category != domain.CategoryRed {
		t.Fatalf("представление = %+v", view)
	}

	if _, err := handler.Handle(ctx, query.GetBasis{BasisID: "basis-1", TenantID: "tenant-2"}); !errors.Is(err, ports.ErrBasisNotFound) {
		t.Fatalf("чужой тенант: ошибка = %v, ожидалась ErrBasisNotFound", err)
	}
}
