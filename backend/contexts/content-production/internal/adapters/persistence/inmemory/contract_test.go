package inmemory

import (
	"testing"

	"lead-gen/backend/contexts/content-production/internal/ports"
	"lead-gen/backend/contexts/content-production/internal/ports/contracttest"
)

// TestContentPackageRepositoryContract прогоняет общий contract suite порта
// ContentPackageRepository против in-memory-реализации. Тот же suite проходит и
// против PostgreSQL-адаптера (см. internal/adapters/persistence/postgres).
func TestContentPackageRepositoryContract(t *testing.T) {
	contracttest.RunContentPackageRepositoryContract(t, func(t *testing.T) ports.ContentPackageRepository {
		t.Helper()
		return New().Content()
	})
}

// TestRenderJobRepositoryContract прогоняет общий contract suite порта
// RenderJobRepository против in-memory-реализации.
func TestRenderJobRepositoryContract(t *testing.T) {
	contracttest.RunRenderJobRepositoryContract(t, func(t *testing.T) ports.RenderJobRepository {
		t.Helper()
		return New().RenderJobs()
	})
}
