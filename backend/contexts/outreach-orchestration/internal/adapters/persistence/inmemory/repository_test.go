package inmemory_test

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracttest"
)

// TestCampaignRepositoryContract прогоняет общий contract suite порта
// CampaignRepository против in-memory-реализации (задача 5.1). Тот же suite
// обязан проходить и против PostgreSQL-реализации (задача 5.2).
func TestCampaignRepositoryContract(t *testing.T) {
	contracttest.RunCampaignRepositoryContract(t, func(t *testing.T) ports.CampaignRepository {
		t.Helper()
		return inmemory.New()
	})
}

// TestDraftWindowContract прогоняет общий contract suite загрузки окна черновиков
// против in-memory-реализации (задача 5.1). PostgreSQL-реализация (задача 5.2)
// обязана пройти этот же suite.
func TestDraftWindowContract(t *testing.T) {
	contracttest.RunDraftWindowContract(t, func(t *testing.T) ports.DraftWindowRepository {
		t.Helper()
		return inmemory.New()
	})
}

// TestListCampaignIDs проверяет порт CampaignLister, который использует роль
// chains для перечисления кампаний.
func TestListCampaignIDs(t *testing.T) {
	ctx := context.Background()
	repo := inmemory.New()

	ids, err := repo.ListCampaignIDs(ctx)
	if err != nil {
		t.Fatalf("ListCampaignIDs пустого репозитория: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("пустой репозиторий вернул %d кампаний", len(ids))
	}

	window, err := domain.NewCampaignWindow(time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-list",
		TenantID: "tenant-1",
		Name:     "Кампания перечисления",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if err := repo.Save(ctx, campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ids, err = repo.ListCampaignIDs(ctx)
	if err != nil {
		t.Fatalf("ListCampaignIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "campaign-list" {
		t.Fatalf("ожидалась одна кампания campaign-list, получено %v", ids)
	}
}
