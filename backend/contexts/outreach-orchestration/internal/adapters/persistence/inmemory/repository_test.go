package inmemory_test

import (
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/inmemory"
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
