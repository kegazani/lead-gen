package memory_test

import (
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/contexts/lead-management/internal/ports/contracttest"
)

// Прогоны общих contract suite портов против in-memory-реализаций. Тот же suite
// обязан проходить и против PostgreSQL-реализации.

func TestLeadProfileRepositoryContract(t *testing.T) {
	contracttest.RunLeadProfileRepositoryContract(t, func(t *testing.T) ports.LeadProfileRepository {
		t.Helper()
		return memory.NewLeadProfileRepository()
	})
}

func TestContactRepositoryContract(t *testing.T) {
	contracttest.RunContactRepositoryContract(t, func(t *testing.T) ports.ContactRepository {
		t.Helper()
		return memory.NewContactRepository()
	})
}

func TestSearchJobStoreContract(t *testing.T) {
	contracttest.RunSearchJobStoreContract(t, func(t *testing.T) ports.SearchJobStore {
		t.Helper()
		return memory.NewSearchJobStore()
	})
}

func TestOutboundMessageRepositoryContract(t *testing.T) {
	contracttest.RunOutboundMessageRepositoryContract(t, func(t *testing.T) ports.OutboundMessageRepository {
		t.Helper()
		return memory.NewOutboundMessageRepository()
	})
}

func TestEmailTemplateRepositoryContract(t *testing.T) {
	contracttest.RunEmailTemplateRepositoryContract(t, func(t *testing.T) ports.EmailTemplateRepository {
		t.Helper()
		return memory.NewEmailTemplateRepository()
	})
}

func TestLeadActivityStoreContract(t *testing.T) {
	contracttest.RunLeadActivityStoreContract(t, func(t *testing.T) ports.LeadActivityStore {
		t.Helper()
		return memory.NewLeadActivityStore()
	})
}
