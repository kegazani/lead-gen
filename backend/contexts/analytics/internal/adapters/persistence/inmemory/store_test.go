package inmemory_test

import (
	"testing"

	"lead-gen/backend/contexts/analytics/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/ports/contracttest"
)

// TestProjectionStoreContract прогоняет общий contract suite порта
// ProjectionStore против in-memory-реализации: тот же набор проверок проходит и
// против PostgreSQL-реализации.
func TestProjectionStoreContract(t *testing.T) {
	contracttest.RunProjectionStoreContract(t, func(t *testing.T) ports.ProjectionStore {
		t.Helper()
		return inmemory.New()
	})
}
