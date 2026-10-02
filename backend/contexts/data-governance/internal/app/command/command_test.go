package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/app/command"
	"lead-gen/backend/contexts/data-governance/internal/domain"
)

var (
	ctx      = context.Background()
	baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

// createBasis создаёт основание через use case и возвращает идентификатор.
func createBasis(t *testing.T, repo *memoryRepo) command.CreateBasisHandler {
	t.Helper()
	handler := command.NewCreateBasisHandler(repo, fixedClock{at: baseTime}, &seqIDs{})
	_, err := handler.Handle(ctx, command.CreateBasis{
		BasisID:       "basis-1",
		TenantID:      "tenant-1",
		Purpose:       "outreach",
		Category:      domain.CategoryGreen,
		RetentionDays: 90,
		ValidFrom:     baseTime,
		ValidUntil:    baseTime.AddDate(0, 0, 90),
		Actor:         "actor-1",
	})
	if err != nil {
		t.Fatalf("создание основания: %v", err)
	}
	return *handler
}

// TestCreateBasis проверяет создание основания и эмиссию события.
func TestCreateBasis(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	createBasis(t, repo)

	basis, err := repo.FindByID(ctx, "basis-1")
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if basis.Status() != domain.BasisActive || basis.TenantID() != "tenant-1" {
		t.Fatalf("основание = %+v", basis)
	}
}

// TestCreateBasisRejectsEmptyPurpose проверяет отказ домена на некорректный вход.
func TestCreateBasisRejectsEmptyPurpose(t *testing.T) {
	t.Parallel()

	handler := command.NewCreateBasisHandler(newMemoryRepo(), fixedClock{at: baseTime}, &seqIDs{})
	_, err := handler.Handle(ctx, command.CreateBasis{
		BasisID: "basis-1", TenantID: "tenant-1", Purpose: "   ", Category: domain.CategoryGreen,
		RetentionDays: 90, ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 90), Actor: "actor-1",
	})
	if !errors.Is(err, domain.ErrEmptyPurpose) {
		t.Fatalf("ошибка = %v, ожидалась ErrEmptyPurpose", err)
	}
}

// TestChangeAuthority проверяет изменение условий основания.
func TestChangeAuthority(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	createBasis(t, repo)
	handler := command.NewChangeAuthorityHandler(repo, fixedClock{at: baseTime}, &seqIDs{})

	result, err := handler.Handle(ctx, command.ChangeAuthority{
		BasisID: "basis-1", TenantID: "tenant-1", RetentionDays: 180,
		ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 180), Actor: "actor-1", Reason: "продление",
	})
	if err != nil {
		t.Fatalf("изменение: %v", err)
	}
	if result.Version != 2 || len(result.Events) != 1 {
		t.Fatalf("результат = %+v", result)
	}
	basis, _ := repo.FindByID(ctx, "basis-1")
	if basis.Retention().Days() != 180 {
		t.Fatalf("срок = %d", basis.Retention().Days())
	}
}

// TestChangeAuthorityTenantMismatch проверяет изоляцию тенантов.
func TestChangeAuthorityTenantMismatch(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	createBasis(t, repo)
	handler := command.NewChangeAuthorityHandler(repo, fixedClock{at: baseTime}, &seqIDs{})
	_, err := handler.Handle(ctx, command.ChangeAuthority{
		BasisID: "basis-1", TenantID: "tenant-2", RetentionDays: 180,
		ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 180), Actor: "actor-1", Reason: "продление",
	})
	if !errors.Is(err, command.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}
}

// TestRevokeBasis проверяет отзыв основания.
func TestRevokeBasis(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	createBasis(t, repo)
	handler := command.NewRevokeBasisHandler(repo, fixedClock{at: baseTime}, &seqIDs{})

	result, err := handler.Handle(ctx, command.RevokeBasis{
		BasisID: "basis-1", TenantID: "tenant-1", Actor: "actor-1", Reason: "отзыв клиента",
		At: baseTime.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("отзыв: %v", err)
	}
	if result.Status != domain.BasisRevoked {
		t.Fatalf("статус = %q", result.Status)
	}
}

// TestRevokeBasisRequiresReason проверяет обязательность причины.
func TestRevokeBasisRequiresReason(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	createBasis(t, repo)
	handler := command.NewRevokeBasisHandler(repo, fixedClock{at: baseTime}, &seqIDs{})
	_, err := handler.Handle(ctx, command.RevokeBasis{
		BasisID: "basis-1", TenantID: "tenant-1", Actor: "actor-1", Reason: " ",
	})
	if !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
}

// TestSuppressAndLift проверяет внесение и снятие записи стоп-листа.
func TestSuppressAndLift(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	createBasis(t, repo)
	suppress := command.NewSuppressContactHandler(repo, fixedClock{at: baseTime}, &seqIDs{})
	if _, err := suppress.Handle(ctx, command.SuppressContact{
		BasisID: "basis-1", TenantID: "tenant-1", SuppressionID: "sup-1",
		Contact: "a@example.com", Reason: "отзыв согласия", Actor: "actor-1",
	}); err != nil {
		t.Fatalf("внесение: %v", err)
	}
	basis, _ := repo.FindByID(ctx, "basis-1")
	if _, ok := basis.IsSuppressed("a@example.com"); !ok {
		t.Fatal("запись не найдена")
	}

	lift := command.NewLiftSuppressionHandler(repo, fixedClock{at: baseTime}, &seqIDs{})
	if _, err := lift.Handle(ctx, command.LiftSuppression{
		BasisID: "basis-1", TenantID: "tenant-1", SuppressionID: "sup-1",
		Actor: "actor-1", Reason: "согласие восстановлено", At: baseTime.Add(time.Hour),
	}); err != nil {
		t.Fatalf("снятие: %v", err)
	}
	basis, _ = repo.FindByID(ctx, "basis-1")
	if _, ok := basis.IsSuppressed("a@example.com"); ok {
		t.Fatal("запись осталась активной")
	}
}

// TestExpireAuthorities проверяет фоновое истечение полномочий роли worker.
func TestExpireAuthorities(t *testing.T) {
	t.Parallel()

	repo := newMemoryRepo()
	create := command.NewCreateBasisHandler(repo, fixedClock{at: baseTime}, &seqIDs{})
	if _, err := create.Handle(ctx, command.CreateBasis{
		BasisID: "basis-1", TenantID: "tenant-1", Purpose: "outreach", Category: domain.CategoryGreen,
		RetentionDays: 1, ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 1), Actor: "actor-1",
	}); err != nil {
		t.Fatalf("создание: %v", err)
	}

	expire := command.NewExpireAuthoritiesHandler(repo, fixedClock{at: baseTime}, &seqIDs{})
	result, err := expire.Handle(ctx, command.ExpireAuthorities{At: baseTime.AddDate(0, 0, 2)})
	if err != nil {
		t.Fatalf("истечение: %v", err)
	}
	if result.Expired != 1 || len(result.Events) != 1 {
		t.Fatalf("результат = %+v", result)
	}
	basis, _ := repo.FindByID(ctx, "basis-1")
	if basis.Status() != domain.BasisRevoked {
		t.Fatalf("статус = %q", basis.Status())
	}
}
