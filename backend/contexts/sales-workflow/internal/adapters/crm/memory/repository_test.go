package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newDeal(t *testing.T) *domain.Deal {
	t.Helper()
	external, err := domain.NewExternalVersion(domain.ExternalDealRef("crm-1"), 1)
	if err != nil {
		t.Fatalf("NewExternalVersion: %v", err)
	}
	deal, err := domain.NewDeal(domain.NewDealParams{
		ID:         domain.DealID("deal-1"),
		TenantID:   domain.TenantID("tenant-1"),
		CompanyRef: domain.CompanyRef("company-1"),
		External:   external,
		Actor:      domain.ActorID("operator-1"),
		At:         testNow,
		EventID:    domain.EventID("evt-1"),
	})
	if err != nil {
		t.Fatalf("NewDeal: %v", err)
	}
	return deal
}

func TestRepositorySaveFindAndIsolation(t *testing.T) {
	repo := New()
	deal := newDeal(t)
	if err := repo.Save(context.Background(), deal, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := repo.FindByID(context.Background(), "deal-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.Stage() != domain.StageLead || loaded.Version() != 1 {
		t.Fatalf("загружено %+v", loaded.Snapshot())
	}

	// Мутация возвращённого агрегата не меняет сохранённое состояние.
	if _, err := loaded.ChangeStage(domain.ActionInput{
		Actor: "operator-1", At: testNow, EventID: "evt-2",
	}, domain.StageQualified); err != nil {
		t.Fatalf("ChangeStage: %v", err)
	}
	again, err := repo.FindByID(context.Background(), "deal-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if again.Stage() != domain.StageLead {
		t.Fatalf("сохранённое состояние изменилось: %s", again.Stage())
	}
}

func TestRepositoryVersionConflict(t *testing.T) {
	repo := New()
	deal := newDeal(t)
	if err := repo.Save(context.Background(), deal, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Save(context.Background(), deal, 0); !errors.Is(err, ports.ErrDealVersionConflict) {
		t.Fatalf("повтор с ожиданием 0: %v, ожидалась ErrDealVersionConflict", err)
	}
	if err := repo.Save(context.Background(), deal, 1); err != nil {
		t.Fatalf("Save с верной версией: %v", err)
	}
}

func TestRepositoryNotFoundAndList(t *testing.T) {
	repo := New()
	if _, err := repo.FindByID(context.Background(), "missing"); !errors.Is(err, ports.ErrDealNotFound) {
		t.Fatalf("отсутствующая сделка: %v", err)
	}

	if err := repo.Save(context.Background(), newDeal(t), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	list, err := repo.ListByStage(context.Background(), domain.StageLead)
	if err != nil {
		t.Fatalf("ListByStage: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("выборка по этапу: %d", len(list))
	}
}
