package memory_test

import (
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/contexts/lead-management/internal/ports/contracttest"
)

// TestAssessmentStoreContract прогоняет общий contract suite порта
// LeadAssessmentStore против in-memory-реализации. Тот же suite обязан проходить
// и против PostgreSQL-реализации.
func TestAssessmentStoreContract(t *testing.T) {
	contracttest.RunAssessmentStoreContract(t, func(t *testing.T) ports.LeadAssessmentStore {
		t.Helper()
		return memory.NewAssessmentStore()
	})
}
